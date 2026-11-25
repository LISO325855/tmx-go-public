package api

import (
	"context"
	"net/http"
	"runtime"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/desktop"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
	"github.com/blackcode-ch/tmx-go/internal/team"
	"github.com/blackcode-ch/tmx-go/internal/update"
)

// Team status (DESIGN.md "Team status"): this computer's status report, collected here because
// the server holds every part of it (the fleet, WhatsApp, the paired phones, the owner gate,
// the accounts, the updater, Companion), and the Team page's routes. Owner only; 503 while
// peer-to-peer is off. The report carries states and counts only: see internal/team for what
// never goes in. Ported from tmx-go v0.1.20's api/team.go; the routes live under
// /api/team-status because /api/team is People's here (people.go).
func (s *Server) teamStatusRoutes(mux *http.ServeMux) {
	o := func(h http.HandlerFunc) http.HandlerFunc { return s.p2pOn(s.ownerOnly(h)) }
	mux.HandleFunc("GET /api/team-status", o(s.getTeam))
	mux.HandleFunc("GET /api/team-status/report", o(s.getTeamReport))
	mux.HandleFunc("PUT /api/team-status/{chat}/share", o(s.putTeamShare))
	mux.HandleFunc("PUT /api/team-status/{chat}/open", o(s.putTeamOpen))
}

func (s *Server) getTeam(w http.ResponseWriter, r *http.Request) {
	p, err := s.chats.Team()
	s.reply(w, http.StatusOK, p, err)
}

// GET /api/team-status/report?chat=ID: this computer's report as that group's admin would get it now.
func (s *Server) getTeamReport(w http.ResponseWriter, r *http.Request) {
	rep, err := s.chats.TeamReport(r.URL.Query().Get("chat"))
	s.reply(w, http.StatusOK, rep, err)
}

// PUT /api/team-status/{chat}/share {share}: share this computer's status with the group's admin.
func (s *Server) putTeamShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Share bool `json:"share"`
	}
	if !decode(w, r, &req) {
		return
	}
	sh, err := s.chats.SetTeamShare(r.PathValue("chat"), req.Share)
	s.reply(w, http.StatusOK, sh, err)
}

// PUT /api/team-status/{chat}/open {open}: the admin lets every member who shares see the whole team.
func (s *Server) putTeamOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Open bool `json:"open"`
	}
	if !decode(w, r, &req) {
		return
	}
	sh, err := s.chats.SetTeamOpen(r.PathValue("chat"), req.Open)
	s.reply(w, http.StatusOK, sh, err)
}

// ---- collecting this computer's report

// teamState is what the collector keeps between reports: when the daemon started and today's
// activity counts.
type teamState struct {
	started int64
	meter   team.Meter // today's activity: two counts, fed when the report is built (at most once a minute)
}

// startTeam hands the chat layer this computer's report. There is no loop of its own: the
// chat layer asks for the report about once a minute while a group shares, and that call is
// the activity meter's clock.
func (s *Server) startTeam() {
	s.team = &teamState{started: model.Now()}
	if s.chats == nil || !s.chats.Enabled() {
		return
	}
	s.chats.SetTeamSource(s.teamReport)
}

// emailState: connected when Companion is linked (it reads and sends the owner's e-mail). This
// build keeps no list of Claude Code's own e-mail connectors, so e-mail set up only there
// reads as not set here: a known gap, never a guess.
func (s *Server) emailState() team.Email {
	if s.companionState() == team.Connected {
		return team.Email{State: team.Connected, Via: team.ViaCompanion}
	}
	return team.Email{State: team.NotSet}
}

// teamReport builds this computer's report (the chat layer adds the links and sent_at).
func (s *Server) teamReport() team.Report {
	r := team.Report{Report: team.ReportVersion, Contract: team.ContractVersion, Links: []team.Link{}}
	r.Node = team.Node{Label: s.f.Machine(), OS: runtime.GOOS, Build: s.f.Version(), BasedOn: team.FleetLine, // its own release line: not compared with upstream's floor
		Personal: desktop.Available, Update: s.updateMode(), StartedAt: s.team.started}
	// Working hours are not configured in this build: left out, which reads as "not set".
	r.Fleet.AIAccounts = map[string]string{}
	if snap, err := s.f.Snapshot(); err == nil {
		all, working := len(snap.Agents), 0
		for _, a := range snap.Agents {
			switch a.State {
			case model.StateStarting, model.StateWorking, model.StateIdle, model.StateWaiting, model.StatePaused:
				r.Fleet.AgentsRunning++
			}
			if a.State == model.StateWorking {
				working++
			}
		}
		r.Fleet.WaitingForOwner = len(snap.Approvals)
		// Counts only: how many agents this computer has and how many are in a turn now.
		r.Fleet.Agents, r.Fleet.AgentsWorking = &all, &working
		// The meter counts this minute once, however often the report is asked for. It runs
		// only while the chat layer asks (a group shares its status), so on the day sharing is
		// switched on the count starts from then.
		s.team.meter.Tick(time.Now(), working)
	}
	act := s.team.meter.Activity(time.Now())
	r.Activity = &act
	lastErr := ""
	if s.accounts != nil {
		for _, a := range s.accounts.Payload().Accounts {
			if !a.Installed {
				continue
			}
			st := team.None
			if a.Connected {
				st = team.SignedIn
			}
			r.Fleet.AIAccounts[a.Harness] = st
			if a.Error != "" && lastErr == "" {
				lastErr = "accounts"
			}
		}
	}
	r.Phone = team.Phone{State: team.NotSet, OwnerGate: team.GateOff}
	// Paired phones and browsers: a count. This build keeps no last-seen time or push state per
	// device that the report could carry, so those two stay at their zero ("not reported").
	if r.Phone.Paired = s.pairedDevices(); r.Phone.Paired > 0 {
		r.Phone.State = team.Connected
	}
	if s.gate != nil && s.gate.Status().Enabled {
		r.Phone.OwnerGate = team.GateOn
		if s.pin != nil && s.pin.IsSet() {
			r.Phone.OwnerGate = team.GateOnWithPIN
		}
	}
	r.WhatsApp = team.WhatsApp{State: team.NotSet}
	if s.wa != nil && s.wa.Enabled() {
		st := s.wa.Status()
		switch st.State {
		case model.WaConnected, model.WaConnecting:
			r.WhatsApp.State = team.Connected
		case model.WaError, model.WaLoggedOut:
			r.WhatsApp.State = team.Error
			if lastErr == "" {
				lastErr = "whatsapp"
			}
		}
	}
	r.Email = s.emailState()
	r.Companion = &team.Companion{State: s.companionState()}
	if r.Companion.State == team.Error && lastErr == "" {
		lastErr = "companion"
	}
	if r.Email.State == team.Error && lastErr == "" {
		lastErr = "email"
	}
	r.Health = team.Health{DiskOK: diskOK(s.f.Paths().Home), MemoryOK: memoryOK()}
	if s.updater != nil && s.updater.Status().State == update.StateError && lastErr == "" {
		lastErr = "update"
	}
	switch {
	case lastErr != "":
	case !r.Health.DiskOK:
		lastErr = "disk"
	case !r.Health.MemoryOK:
		lastErr = "memory"
	}
	r.Health.LastError = lastErr
	return r
}

// updateMode: off (this build cannot update itself), waiting (a release is staged and waits for
// busy agents), auto, or pinned (automatic updates off).
func (s *Server) updateMode() string {
	if s.updater == nil {
		return team.UpdateOff
	}
	st := s.updater.Status()
	switch {
	case st.State == update.StateOff:
		return team.UpdateOff
	case st.State == update.StateReady && len(st.Waiting) > 0:
		return team.UpdateWaiting
	case st.Auto:
		return team.UpdateAuto
	}
	return team.UpdatePinned
}

// diskOK: the disk holding tmx's state has at least 2 GiB and 5 % free (unknown = ok).
func diskOK(path string) bool {
	free, total, err := sysinfo.DiskSpace(path)
	if err != nil || total == 0 {
		return true
	}
	return free >= 2<<30 && free*20 >= total
}

// memoryOK: at least 1 GiB or 10 % of the memory available (unknown = ok).
func memoryOK() bool {
	m, err := sysinfo.MachineStats()
	if err != nil || m.MemTotal == 0 {
		return true
	}
	return m.MemFree >= 1<<30 || m.MemFree*10 >= m.MemTotal
}

// companionState: whether the owner's assistant is linked to this computer (a state, nothing
// of what it does).
func (s *Server) companionState() string {
	if s.companion == nil {
		return team.NotSet
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.companion.Status(ctx, false).Connected {
		return team.Connected
	}
	return team.NotSet
}

// pairedDevices counts the owner's paired phones and browsers that are still valid (not a
// recovery session, not expired) - the same test pushDeviceActive applies to one device.
func (s *Server) pairedDevices() int {
	d := s.devices
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return 0
	}
	n := 0
	for _, dev := range d.state.Devices {
		if !dev.Recovery && d.now().Before(dev.Expires) {
			n++
		}
	}
	return n
}
