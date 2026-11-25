// Package api is the daemon's HTTP + WebSocket server (DESIGN.md "API"): owner and agent
// bearer-token auth, CORS for the Wails origins and the daemon's own, JSON errors, the
// event and terminal sockets, and the embedded UI at /.
package api

import (
	"context"
	"errors"
	"fmt"
	"github.com/blackcode-ch/tmx-go/internal/transfers"
	"github.com/blackcode-ch/tmx-go/internal/update"
	"github.com/blackcode-ch/tmx-go/internal/uploads"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/accounts"
	"github.com/blackcode-ch/tmx-go/internal/chat"
	"github.com/blackcode-ch/tmx-go/internal/claudeext"
	"github.com/blackcode-ch/tmx-go/internal/companion"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/gate"
	"github.com/blackcode-ch/tmx-go/internal/jobs"
	"github.com/blackcode-ch/tmx-go/internal/machinehealth"
	"github.com/blackcode-ch/tmx-go/internal/ownerpin"
	"github.com/blackcode-ch/tmx-go/internal/p2p"
	"github.com/blackcode-ch/tmx-go/internal/peers"
	"github.com/blackcode-ch/tmx-go/internal/recorder"
	"github.com/blackcode-ch/tmx-go/internal/recworker"
	"github.com/blackcode-ch/tmx-go/internal/reshistory"
	"github.com/blackcode-ch/tmx-go/internal/resources"
	"github.com/blackcode-ch/tmx-go/internal/restartguard"
	"github.com/blackcode-ch/tmx-go/internal/slots"
	"github.com/blackcode-ch/tmx-go/internal/summarize"
	"github.com/blackcode-ch/tmx-go/internal/tailscale"
	"github.com/blackcode-ch/tmx-go/internal/transcribe"
	"github.com/blackcode-ch/tmx-go/internal/whatsapp"
)

// MaxBody is the request body limit.
const MaxBody = 1 << 20

// Options configures the server.
type Options struct {
	AudioGuard *restartguard.Guard // shared with the updater; nil creates a local guard
	Transfers  *transfers.Service
	// OpenAgentDesktop can be supplied by an embedding desktop; nil uses the OS.
	OpenAgentDesktop func(kind, target string) error
	SystemWarnings   []string
	Jobs             *jobs.Manager
	TrustedHosts     []string // exact HTTPS proxy hosts; empty = local only
	Fleet            *fleet.Fleet
	OwnerToken       string
	Port             int           // the daemon's port, for the allowed own origin
	Shutdown         func()        // POST /api/shutdown (owner); nil = route answers 501
	Restart          func()        // POST /api/fleet-ops/restart (head, leads): stop and start again; nil = 501
	Gate             *gate.Gate    // the owner gate; nil = none (gated actions just go ahead)
	Chats            *chat.Service // chats with other tmx installs; nil or without a node = p2p off (503)
	P2P              p2p.Node      // the peer-to-peer node; nil = off
	P2PLog           *p2p.Drain    // config "p2p.debug_drain": the network log; nil = off
	Logger           *slog.Logger
	// PhoneLock is config.json "phone_lock" (phonelock.go); nil = the default (on).
	PhoneLock *config.PhoneLockConfig
	// LANAccess is config.json api.lan_access: the same Wi-Fi fallback (phonelan.go).
	LANAccess bool

	Resources *resources.Manager // GET /api/resources ...; nil = those routes answer 501
	History   *reshistory.Store  // GET /api/resources/history (reshistory.go); nil = 501
	// Health reads temperatures, drive health and the GPU for GET /api/machine/health; nil = 501.
	Health    *machinehealth.Service
	Slots     *slots.Manager     // /api/slots; nil = 501
	Companion *companion.Service // /api/companion...; nil = 501 (and no Companion caller)
	Uploads   *uploads.Store     // files attached to messages; nil = 501
	// SaveP2PName persists the display name people see in chats (config.json p2p.name).
	SaveP2PName func(name string) error
	Updater     *update.Updater   // /api/update...; nil = 501
	Recorder    *recorder.Service // /api/recordings...; nil = 501

	Voice *transcribe.Service // /api/voice, /api/transcribe, /api/transcripts; nil = 501

	Summaries *summarize.Service // /api/recordings/{id}/summary, /api/summaries...; nil = 501
	RecWorker *recworker.Service // POST /api/recordings/{id}/process (the report worker); nil = 501
	WhatsApp  *whatsapp.Service  // /api/whatsapp...; nil = 501
	Accounts  *accounts.Service  // /api/accounts...; nil = 501
	ClaudeExt *claudeext.Service // /api/claude/...; nil = 501
	Peers     *peers.Client      // the Python fleet interop (peers.go); nil = no peer routes
}

type Server struct {
	audioGuard     *restartguard.Guard
	restartNotice  restartNoticeState
	transfers      *transfers.Service
	push           phonePush
	agentDesktop   agentDesktop
	p2pActions     p2pActionStore
	systemWarnings []string
	recoveryNotice func(string, time.Time) error
	devices        *deviceStore
	trustedMu      sync.RWMutex
	trustedHosts   []string     // exact HTTPS proxy hosts (trusted.go); read with TrustedHosts()
	pairing        pairingCodes // the Connect-a-phone code shown now (phonecode.go)
	tsFind         func() *tailscale.Client
	tsProbe        func(ctx context.Context, url string) error
	tsCheck        tsCheckCache
	lock           phoneLock // phonelock.go
	f              *fleet.Fleet
	owner          string
	port           int
	stop           func()
	selfRestart    func() // fleetops.go
	gate           *gate.Gate
	chats          *chat.Service
	p2p            p2p.Node
	p2pLogs        *p2p.Drain
	log            *slog.Logger
	uploads        *uploads.Store
	saveP2PName    func(string) error
	updater        *update.Updater
	handler        http.Handler
	res            *resources.Manager
	hist           *reshistory.Store
	hw             *machinehealth.Service
	team           *teamState  // this computer's team status report (teamstatus.go)
	peerEditMu     *sync.Mutex // serializes peer config/store changes and invite binding
	join           joinState   // the live code for a new machine to join the fleet (machinejoin.go)
	slots          *slots.Manager
	slotBootID     string
	jobs           *jobs.Manager

	companion *companion.Service
	linkRate  linkLimiter
	voice     *transcribe.Service
	rec       *recorder.Service
	sum       *summarize.Service
	recw      *recworker.Service
	wa        *whatsapp.Service
	accounts  *accounts.Service
	claudeExt *claudeext.Service
	peers     *peers.Client
	peerMux   http.Handler
	pin       *ownerpin.Store // the owner PIN (ownerpin.go)

	// the same Wi-Fi fallback (phonelan.go)
	lan           atomic.Bool
	lanMu         sync.Mutex
	lanHS         *http.Server
	lanLns        map[string]net.Listener // network address -> its listener
	lanFailed     map[string]bool         // addresses that could not be bound (logged once)
	lanListen     func(addr string) (net.Listener, error)
	phoneAddrs    func() []PhoneAddress
	firewallAllow func(ctx context.Context, port int) (string, error)
}

func New(o Options) *Server {
	if o.AudioGuard == nil {
		o.AudioGuard = &restartguard.Guard{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	s := &Server{f: o.Fleet, owner: o.OwnerToken, port: o.Port, stop: o.Shutdown, selfRestart: o.Restart, gate: o.Gate, log: o.Logger,
		res: o.Resources, slots: o.Slots, chats: o.Chats, p2p: o.P2P, p2pLogs: o.P2PLog, companion: o.Companion, uploads: o.Uploads, saveP2PName: o.SaveP2PName, updater: o.Updater,
		rec: o.Recorder}
	s.slotBootID = newSlotBootID()
	s.agentDesktop.open = o.OpenAgentDesktop
	s.audioGuard = o.AudioGuard
	if s.agentDesktop.open == nil {
		s.agentDesktop.open = openAgentDesktop
	}
	s.voice = o.Voice
	s.systemWarnings = append([]string{}, o.SystemWarnings...)
	s.jobs = o.Jobs
	s.trustedHosts = append([]string(nil), o.TrustedHosts...)
	s.lock.set(o.PhoneLock.Resolved())
	s.wireGate() // safety.go: gate.updated events
	s.sum = o.Summaries
	s.recw = o.RecWorker
	s.wa = o.WhatsApp
	s.accounts = o.Accounts
	s.claudeExt = o.ClaudeExt
	s.peers = o.Peers
	s.peerEditMu = &sync.Mutex{}
	if s.f != nil {
		s.peerEditMu = peers.MutationMutex(s.f.Paths().PeersFile())
	}
	s.hist = o.History
	s.hw = o.Health
	s.transfers = o.Transfers
	s.peerMux = s.peerProtocolRoutes()
	s.initDevices()
	s.startTeam() // teamstatus.go: this computer's report for the chat layer
	s.lan.Store(o.LANAccess)
	if s.devices != nil && !o.LANAccess {
		s.devices.clearLAN() // switched off (also by hand in config.json): no device paired over it stays
	}
	s.initPush()
	if s.gate != nil && s.pin != nil {
		s.gate.SetPINCheck(s.pin.IsSet) // #187: the PIN answers every confirmation, also at the machine
	}
	if s.f != nil {
		s.recoveryNotice = s.f.OwnerRecoveryNotice
	}
	mux := http.NewServeMux()
	s.deviceRoutes(mux)
	s.pushRoutes(mux)
	s.fleetOpsRoutes(mux) // fleetops.go
	s.restartNoticeRoutes(mux)
	s.routes(mux)
	s.handler = s.guard(mux)
	return s
}

// Handler is the full handler (CORS, host check, auth, routes).
func (s *Server) Handler() http.Handler { return s.handler }

// Listen binds the daemon's port on 127.0.0.1 only.
func Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}

// Serve serves on ln until ctx is cancelled, then shuts down gracefully.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	hs := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	go s.pinSyncLoop(ctx)     // ownerpin.go: the owner PIN from the fleet's other machines
	go s.lockLoop(ctx.Done()) // phonelock.go: phones whose lock came due
	go s.lanLoop(ctx, hs)     // phonelan.go: the extra listeners while the same Wi-Fi fallback is on
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := hs.Shutdown(sctx)
	if errors.Is(err, context.DeadlineExceeded) {
		err = hs.Close() // WebSockets are hijacked; Close ends the rest
	}
	if e := <-errc; e != nil && !errors.Is(e, http.ErrServerClosed) {
		return e
	}
	return err
}
