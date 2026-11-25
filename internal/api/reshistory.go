package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/machinehealth"
	"github.com/blackcode-ch/tmx-go/internal/peers"
	"github.com/blackcode-ch/tmx-go/internal/reshistory"
)

// The Watchdog page's history (internal/reshistory): this machine's own, and - for the owner -
// another machine's, read by this daemon with the pair token it shares with that machine.

// maxHistoryPoints bounds what is passed on from another machine: the longest period this
// daemon itself answers is a day of minutes.
const maxHistoryPoints = 2000

// maxHistoryDisks bounds the per-disk series passed on from another machine.
const maxHistoryDisks = 16

func (s *Server) historyRoutes(mux *http.ServeMux) {
	// Like GET /api/resources: readings of the machine, no content; any authenticated caller,
	// which is also how a paired machine reads it for its owner's window.
	mux.HandleFunc("GET /api/resources/history", s.resourceHistory)
	mux.HandleFunc("GET /api/machines/{name}/history", s.ownerOnly(s.machineHistory))
	// The machine's hardware (internal/machinehealth): temperatures, drive health, the GPU.
	mux.HandleFunc("GET /api/machine/health", s.machineHealth)
	mux.HandleFunc("GET /api/machines/{name}/health", s.ownerOnly(s.peerMachineHealth))
}

// machineHealth is GET /api/machine/health: read on demand, cached for half a minute.
func (s *Server) machineHealth(w http.ResponseWriter, r *http.Request) {
	if s.hw == nil {
		writeError(w, http.StatusNotImplemented, "machine health is not read in this daemon")
		return
	}
	writeJSON(w, http.StatusOK, s.hw.View(r.Context()))
}

// peerMachineHealth is GET /api/machines/{name}/health: the same from one of the owner's other
// machines, asked by this daemon with their pair token (a fixed path: the caller chooses the
// machine only). What comes back is that machine's display data: cleaned before it is passed on.
func (s *Server) peerMachineHealth(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.PathValue("name"))
	if s.peers == nil || !s.peers.Known(name) {
		writeError(w, http.StatusNotFound, "no other machine "+clampText(name, 40))
		return
	}
	var v machinehealth.View
	if err := s.peers.Call(r.Context(), name, http.MethodGet, "/api/machine/health", nil, &v); err != nil {
		var refused peers.Refused
		switch {
		case errors.As(err, &refused) && (refused.Status == http.StatusNotFound || refused.Status == http.StatusNotImplemented):
			writeError(w, http.StatusBadGateway, name+" does not answer its hardware readings to this window (it needs the Go version of tmx)")
		case errors.As(err, &refused):
			writeError(w, http.StatusBadGateway, name+" refused: "+clampText(refused.Msg, 200))
		default:
			writeError(w, http.StatusBadGateway, name+" does not answer")
		}
		return
	}
	writeJSON(w, http.StatusOK, v.Clean())
}

// resourceHistory is GET /api/resources/history?period=1h|6h|24h|7d (default 1h).
func (s *Server) resourceHistory(w http.ResponseWriter, r *http.Request) {
	if s.hist == nil {
		writeError(w, http.StatusNotImplemented, "resource history is not running in this daemon")
		return
	}
	v, ok := s.hist.View(historyPeriod(r))
	if !ok {
		writeError(w, http.StatusBadRequest, "period: 1h, 6h, 24h or 7d")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func historyPeriod(r *http.Request) string {
	if p := r.URL.Query().Get("period"); p != "" {
		return p
	}
	return "1h"
}

// machineHistory is GET /api/machines/{name}/history?period=…: the same reading from one of
// the owner's other machines. This daemon asks that machine itself (its pair token, a fixed
// path, one of the four periods) - the caller chooses the machine and the period, nothing else
// of the request. What comes back is another machine's display data: decoded into the known
// shape (anything else is dropped) and cut to a bounded number of points.
func (s *Server) machineHistory(w http.ResponseWriter, r *http.Request) {
	name, period := strings.ToLower(r.PathValue("name")), historyPeriod(r)
	if !reshistory.ValidPeriod(period) {
		writeError(w, http.StatusBadRequest, "period: 1h, 6h, 24h or 7d")
		return
	}
	if s.peers == nil || !s.peers.Known(name) {
		writeError(w, http.StatusNotFound, "no other machine "+clampText(name, 40))
		return
	}
	var v reshistory.View
	if err := s.peers.Call(r.Context(), name, http.MethodGet, "/api/resources/history?period="+url.QueryEscape(period), nil, &v); err != nil {
		var refused peers.Refused
		switch {
		case errors.As(err, &refused) && (refused.Status == http.StatusNotFound || refused.Status == http.StatusNotImplemented):
			writeError(w, http.StatusBadGateway, name+" keeps no resource history this window can read (it needs the Go version of tmx)")
		case errors.As(err, &refused):
			writeError(w, http.StatusBadGateway, name+" refused: "+clampText(refused.Msg, 200))
		default:
			writeError(w, http.StatusBadGateway, name+" does not answer")
		}
		return
	}
	v.Period, v.Day = period, clampText(v.Day, 10)
	if len(v.Points) > maxHistoryPoints {
		v.Points = v.Points[len(v.Points)-maxHistoryPoints:]
	}
	if v.Points == nil {
		v.Points = []reshistory.Point{}
	}
	disks := []reshistory.DiskSeries{}
	for _, d := range v.Disks {
		if len(disks) == maxHistoryDisks {
			break
		}
		d.Name = clampText(d.Name, 64)
		if d.Name == "" {
			continue
		}
		if len(d.Points) > maxHistoryPoints {
			d.Points = d.Points[len(d.Points)-maxHistoryPoints:]
		}
		disks = append(disks, d)
	}
	v.Disks = disks
	writeJSON(w, http.StatusOK, v)
}
