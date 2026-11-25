package api

// The same Wi-Fi fallback for Connect a phone (config.json api.lan_access; off by default,
// switched on per install by the owner at the computer). Ported from Mustneer's v0.1.20
// lan.go and the Wi-Fi half of his phone.go, cut down to what a fallback needs.
//
// While it is on, besides 127.0.0.1 the daemon also listens on this machine's private network
// addresses (Wi-Fi, Ethernet; never a VPN or a virtual adapter), over plain http, so a phone
// on the same network can open http://<address>:<port>/#pair=<code> without Tailscale. The
// loopback listener is never touched. It exists so the owner is never stuck; Tailscale stays
// the way that is offered first.
//
// What a request that arrives on a network address may do is narrower than anywhere else:
//   - only with the fallback on, and only with the exact address it arrived on as Host (a
//     DNS rebinding attack needs a name);
//   - no bearer token of any kind is accepted (owner, agent, Companion, peers): they are
//     for this machine, and would cross the network in clear text;
//   - a browser pairs only with a Connect-a-phone code AND the owner PIN (phonecode.go);
//   - a device paired this way has its own cookie (not Secure: there is no https here),
//     valid only on that address while the fallback is on. Switching it off closes the
//     listeners, ends those sessions and removes those devices.
//
// Browser rules, not ours: without https the phone has no dictation, no notifications, no
// fingerprint unlock and cannot be installed as an app.

import (
	"context"
	"net"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/gate"
)

// lanRescan is how often the network addresses are re-read while the fallback is on.
const lanRescan = 30 * time.Second

// lanCookie is a LAN-paired device's session cookie (plus "-<port>").
const lanCookie = "tmx-lan-device"

// SetPhoneAddrs replaces how the phone addresses are found (tests).
func (s *Server) SetPhoneAddrs(fn func() []PhoneAddress) { s.phoneAddrs = fn }

func (s *Server) wifiAddrs() []PhoneAddress {
	if s.phoneAddrs != nil {
		return s.phoneAddrs()
	}
	return phoneAddrs()
}

// LANOn reports whether the same Wi-Fi fallback is on.
func (s *Server) LANOn() bool { return s.lan.Load() }

// SetLAN turns the fallback on or off now (no restart needed). Off also removes every device
// paired over it and ends the requests and sockets still open over the network.
func (s *Server) SetLAN(on bool) {
	s.lan.Store(on)
	s.syncLAN()
	if !on && s.devices != nil {
		s.devices.clearLAN()
	}
}

// clearLAN removes the devices paired over the network and ends what they have open.
func (d *deviceStore) clearLAN() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !slices.ContainsFunc(d.state.Devices, func(v ownerDevice) bool { return v.LAN }) {
		return
	}
	before := d.before()
	var gone []string
	d.state.Devices = slices.DeleteFunc(d.state.Devices, func(v ownerDevice) bool {
		if v.LAN {
			gone = append(gone, v.ID)
		}
		return v.LAN
	})
	if d.save(before) != nil {
		return // still listed; their sessions are refused anyway while the fallback is off
	}
	for _, id := range gone {
		for _, cancel := range d.active[id] {
			cancel()
		}
		delete(d.active, id)
		delete(d.pinSess, id)
	}
}

// lanLoop keeps the network listeners in step with the setting and the network until ctx ends.
func (s *Server) lanLoop(ctx context.Context, hs *http.Server) {
	s.lanMu.Lock()
	s.lanHS = hs
	s.lanMu.Unlock()
	s.syncLAN()
	t := time.NewTicker(lanRescan)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.lanMu.Lock()
			for ip, ln := range s.lanLns {
				ln.Close()
				delete(s.lanLns, ip)
			}
			s.lanHS = nil
			s.lanMu.Unlock()
			return
		case <-t.C:
			if s.lan.Load() {
				s.syncLAN()
			}
		}
	}
}

// syncLAN opens a listener on every current private address while the fallback is on, and
// closes the ones that are no longer wanted.
func (s *Server) syncLAN() {
	s.lanMu.Lock()
	defer s.lanMu.Unlock()
	if s.lanHS == nil {
		return // not serving yet (or any more); lanLoop syncs when it starts
	}
	if s.lanLns == nil {
		s.lanLns, s.lanFailed = map[string]net.Listener{}, map[string]bool{}
	}
	want := map[string]bool{}
	if s.lan.Load() {
		for _, a := range s.wifiAddrs() {
			want[a.IP] = true
		}
	}
	for ip, ln := range s.lanLns {
		if !want[ip] {
			ln.Close()
			delete(s.lanLns, ip)
			s.log.Info("same Wi-Fi: stopped listening", "addr", ip)
		}
	}
	listen := s.lanListen
	if listen == nil {
		listen = func(addr string) (net.Listener, error) { return net.Listen("tcp4", addr) }
	}
	for ip := range want {
		if _, ok := s.lanLns[ip]; ok {
			continue
		}
		addr := net.JoinHostPort(ip, strconv.Itoa(s.port))
		ln, err := listen(addr)
		if err != nil {
			if !s.lanFailed[ip] {
				s.log.Warn("same Wi-Fi: cannot listen", "addr", addr, "err", err)
			}
			s.lanFailed[ip] = true
			continue
		}
		delete(s.lanFailed, ip)
		s.lanLns[ip] = ln
		hs := s.lanHS
		go func() { _ = hs.Serve(ln) }() // returns when the listener is closed
		s.log.Info("same Wi-Fi: listening", "url", "http://"+addr)
	}
}

// viaLAN reports whether the request arrived on a non-loopback address of this machine.
func viaLAN(r *http.Request) bool {
	a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	t, ok := a.(*net.TCPAddr)
	return ok && !t.IP.IsLoopback()
}

// lanRequest reports whether r is a request the fallback serves: it arrived on a private
// network address, the fallback is on, and its Host is exactly that address and port.
func (s *Server) lanRequest(r *http.Request) bool {
	a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || !s.lan.Load() {
		return false
	}
	t, ok := a.(*net.TCPAddr)
	if !ok || t.IP.IsLoopback() || t.IP.To4() == nil || !t.IP.IsPrivate() {
		return false
	}
	return r.Host == net.JoinHostPort(t.IP.To4().String(), strconv.Itoa(t.Port))
}

// phoneAddrs are this machine's private IPv4 addresses on interfaces that are up and not
// virtual (VPNs, bridges, containers), the Wi-Fi or Ethernet one first.
func phoneAddrs() []PhoneAddress {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := []PhoneAddress{}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagPointToPoint != 0 || virtualIface(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || !n.IP.IsPrivate() {
				continue
			}
			out = append(out, PhoneAddress{IP: n.IP.To4().String(), Interface: ifc.Name})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return ifaceRank(out[i].Interface) < ifaceRank(out[j].Interface) })
	return out
}

var virtualPrefixes = []string{"utun", "tun", "tap", "bridge", "vmnet", "vboxnet", "docker", "br-", "veth", "virbr", "lxc", "lxd", "cni",
	"flannel", "cali", "awdl", "llw", "tailscale", "zt", "wg", "gif", "stf", "anpi", "ap", "ipsec", "feth", "ham", "vEthernet", "VirtualBox", "VMware",
	"WSL", "Hyper-V", "Loopback", "Bluetooth"}

func virtualIface(name string) bool {
	low := strings.ToLower(name)
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(low, strings.ToLower(p)) {
			return true
		}
	}
	return strings.Contains(low, "wsl") || strings.Contains(low, "hyper-v") || strings.Contains(low, "virtual")
}

// ifaceRank: en0 (a Mac's Wi-Fi or built-in Ethernet) first, then other Wi-Fi and Ethernet names.
func ifaceRank(name string) int {
	low := strings.ToLower(name)
	switch {
	case low == "en0":
		return 0
	case strings.HasPrefix(low, "wl") || strings.Contains(low, "wi-fi") || strings.Contains(low, "wifi") || strings.Contains(low, "wireless") || strings.Contains(low, "wlan"):
		return 1
	case strings.HasPrefix(low, "en") || strings.HasPrefix(low, "eth") || strings.Contains(low, "ethernet"):
		return 2
	}
	return 3
}

func (s *Server) phoneLANRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/phone/wifi", s.atComputer(s.putWifi))
}

func (s *Server) wifiStatus() WifiStatus {
	addrs := s.wifiAddrs()
	if addrs == nil {
		addrs = []PhoneAddress{}
	}
	return WifiStatus{On: s.LANOn(), Addresses: addrs}
}

// saveLAN writes api.lan_access and applies it.
func (s *Server) saveLAN(on bool) error {
	if s.f != nil {
		c, err := config.Load(s.f.Paths())
		if err != nil {
			return err
		}
		c.API.LANAccess = on
		if err := config.Save(s.f.Paths(), c); err != nil {
			return err
		}
	}
	s.SetLAN(on)
	s.log.Info("same Wi-Fi: switched", "on", on)
	return nil
}

// putWifi: PUT /api/phone/wifi {on}: the owner's switch for the same Wi-Fi fallback. On is
// behind the owner gate (it opens this machine's port to the local network); off needs no
// more than being the owner at the computer. A missing firewall rule is a 409 {step:
// "firewall"} with the fallback left on, so "Try again" is enough once it is allowed.
func (s *Server) putWifi(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.On && !s.LANOn() {
		if !s.requireOwner(w, r, gate.Action{Reason: "let a phone on the same Wi-Fi reach tmx without Tailscale (plain http on your local network)", Key: actionKey(r, req)}) {
			return
		}
	}
	if req.On != s.LANOn() {
		if err := s.saveLAN(req.On); err != nil {
			writeErr(w, err)
			return
		}
	}
	if req.On {
		if p := s.firewallProblem(r.Context()); p != nil {
			writeJSON(w, http.StatusConflict, p)
			return
		}
	}
	writeJSON(w, http.StatusOK, s.wifiStatus())
}

// firewallProblem makes sure the machine's firewall lets the local network reach the port
// (Windows: one administrator prompt, once per port); nil = nothing in the way that tmx
// knows of.
func (s *Server) firewallProblem(ctx context.Context) *phoneProblem {
	allow := s.firewallAllow
	if allow == nil {
		allow = allowPhonePort
	}
	hint, err := allow(ctx, s.port)
	if err == nil {
		return nil
	}
	s.log.Warn("same Wi-Fi: the firewall is not open", "err", err)
	return &phoneProblem{Step: "firewall", Error: "Windows Firewall still blocks your phone. Click Try again and answer Yes when Windows asks, or run this once in a terminal opened as administrator", Output: hint}
}

func (s *Server) connectWifi(w http.ResponseWriter, r *http.Request, req connectRequest) {
	if !s.LANOn() {
		writeJSON(w, http.StatusConflict, phoneProblem{Step: "off", Error: "Connecting over the same Wi-Fi is switched off on this computer"})
		return
	}
	addrs := s.wifiAddrs()
	if len(addrs) == 0 {
		writeJSON(w, http.StatusConflict, phoneProblem{Step: "network", Error: "this computer has no local network address: connect it to Wi-Fi or a network cable"})
		return
	}
	ip := addrs[0].IP
	if req.Address != "" {
		i := slices.IndexFunc(addrs, func(a PhoneAddress) bool { return a.IP == req.Address })
		if i < 0 {
			writeError(w, http.StatusBadRequest, "“"+req.Address+"” is not one of this computer's network addresses")
			return
		}
		ip = addrs[i].IP
	}
	s.syncLAN() // a new address since the last look
	host := net.JoinHostPort(ip, strconv.Itoa(s.port))
	writeJSON(w, http.StatusCreated, PhoneConnection{Via: "wifi", Pairing: s.issuePairing("http://"+host, host)})
}
