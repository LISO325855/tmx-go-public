package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

type restartNotice struct {
	At int64 `json:"at"` // Unix milliseconds; zero cancels the notice
}
type restartNoticeState struct {
	sync.Mutex
	notice restartNotice
}

func (s *Server) restartNoticeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/restart-notice", s.ownerOnly(s.getRestartNotice))
	mux.HandleFunc("POST /api/restart-notice", s.ownerOnly(s.postRestartNotice))
	mux.HandleFunc("POST /api/fleet-ops/restart-notice", s.headOrLead(s.postRestartNotice))
	mux.HandleFunc("POST /api/audio/capture", s.ownerOnly(s.audioCapture))
}

func (s *Server) getRestartNotice(w http.ResponseWriter, r *http.Request) {
	s.restartNotice.Lock()
	n := s.restartNotice.notice
	s.restartNotice.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, n)
}

// A notice announces intent only: the installer must still request the guarded handover.
func (s *Server) postRestartNotice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seconds int `json:"seconds"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Seconds < 0 || req.Seconds > 3600 {
		writeError(w, 400, "seconds must be between 0 (cancel) and 3600")
		return
	}
	n := restartNotice{}
	if req.Seconds > 0 {
		n.At = time.Now().Add(time.Duration(req.Seconds) * time.Second).UnixMilli()
	}
	s.restartNotice.Lock()
	s.restartNotice.notice = n
	s.restartNotice.Unlock()
	if s.f != nil {
		s.f.Publish(model.BusEvent{Type: "daemon.restart-notice", Data: n})
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) audioCapture(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Active bool   `json:"active"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !transcriptionKey.MatchString(req.ID) {
		writeError(w, 400, "invalid capture id")
		return
	}
	if err := s.audioGuard.Capture(req.ID, req.Active); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) reserveRestart(w http.ResponseWriter, r *http.Request) bool {
	var req struct {
		Force bool `json:"force"`
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return false
	}
	return s.reserveAudioRestart(w, req.Force)
}

func (s *Server) reserveAudioRestart(w http.ResponseWriter, force bool) bool {
	_, err := s.audioGuard.Reserve(force, func() bool { return s.rec != nil && len(s.rec.Busy()) > 0 })
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return false
	}
	return true
}

func (s *Server) captureAdmission(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.audioGuard.Admit(func() { h(w, r) }); err != nil {
			writeError(w, http.StatusConflict, err.Error())
		}
	}
}
