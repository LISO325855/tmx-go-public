package api

import (
	"encoding/json"
	"net/http"

	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/gate"
)

func (s *Server) agentControlRoutes(mux *http.ServeMux) {
	for _, base := range []string{"/api/agents/{id}/", "/api/sessions/{id}/"} {
		mux.HandleFunc("POST "+base+"answer", s.ownerOnly(s.answerAgent))
		mux.HandleFunc("POST "+base+"shelve", s.shelveAgent)
		mux.HandleFunc("POST "+base+"unshelve", s.unshelveAgent)
		mux.HandleFunc("POST "+base+"rename", s.renameAgent)
	}
	// The rest of the session routes the relay admits (remote.go's remoteSessionPath, Python's
	// names), so the owner's window on one Go machine can work an agent on another Go machine
	// exactly as it works one on a Python machine. Each is the SAME handler as its /api/agents
	// twin (routes.go) - same caller, same authority checks, same answer - never a second
	// implementation: a caller without the right to stop an agent there has none here either.
	// "forget" is DELETE /api/agents/{id}: the owner at this machine only, so a relayed request
	// is refused. "open" is already served for both bases (agent_files.go).
	mux.HandleFunc("GET /api/sessions/{id}/screen", s.screen)
	mux.HandleFunc("POST /api/sessions/{id}/keys", s.sendKeys)
	mux.HandleFunc("POST /api/sessions/{id}/stop", s.act((*fleet.Fleet).StopAgent))
	mux.HandleFunc("POST /api/sessions/{id}/start", s.start)
	mux.HandleFunc("POST /api/sessions/{id}/restart", s.restart)
	mux.HandleFunc("POST /api/sessions/{id}/forget", s.ownerOnly(s.removeAgent))
	// "send" is NOT the twin of POST /api/agents/{id}/send (the owner's mail, sent as the owner
	// and for the owner alone). On a Python machine, and for the Python CLI that calls this
	// path (`tmx send name@machine`), it types text into the agent: body {text, submit}. So it
	// is POST /api/agents/{id}/type under Python's name - what a paired machine may already do,
	// with typeText's own checks; nothing it sends ever becomes mail from the owner.
	mux.HandleFunc("POST /api/sessions/{id}/send", s.typeText)
	// POST /api/sessions launches, as POST /api/agents does (Go's own body: fleet.LaunchRequest).
	mux.HandleFunc("POST /api/sessions", s.launch)
}

func (s *Server) renameAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	c, _ := caller(r)
	if c.Owner && !s.requireOwner(w, r, gate.Action{Reason: "rename agent " + r.PathValue("id") + " to " + req.Name, Key: actionKey(r, req)}) {
		return
	}
	a, err := s.f.RenameAgent(c, r.PathValue("id"), req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) shelveAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note string `json:"note"`
	}
	if !decode(w, r, &req) {
		return
	}
	c, _ := caller(r)
	a, err := s.f.ShelveAgent(c, r.PathValue("id"), req.Note)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) unshelveAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Start bool `json:"start"`
	}
	if !decode(w, r, &req) {
		return
	}
	c, _ := caller(r)
	a, err := s.f.UnshelveAgent(c, r.PathValue("id"), req.Start)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) answerAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Choice json.RawMessage `json:"choice"`
	}
	if !decode(w, r, &req) {
		return
	}
	var choice string
	if json.Unmarshal(req.Choice, &choice) != nil {
		var n json.Number
		if json.Unmarshal(req.Choice, &n) != nil || n.String() == "" {
			writeError(w, 400, "choice must be a label or option number")
			return
		}
		choice = n.String()
	}
	c, _ := caller(r)
	ap, d, err := s.f.AgentAnswer(c, r.PathValue("id"), choice)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !s.gateDecide(w, r, ap.ID, d) || !s.gateCompanion(w, r, ap, d) {
		return
	}
	ap, err = s.f.Decide(c, ap.ID, d)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ap)
}
