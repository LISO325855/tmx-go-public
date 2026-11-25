package api

import (
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"net/http"
)

func (s *Server) processRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/processes", s.ownerOnly(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, s.f.ProcessTools()) }))
	mux.HandleFunc("POST /api/processes/requests", s.requestProcess)
	mux.HandleFunc("GET /api/agents/{id}/processes", s.agentProcesses)
	mux.HandleFunc("POST /api/agents/{id}/processes", s.ownerOnly(s.startProcess))
	mux.HandleFunc("POST /api/processes/{id}/stop", s.stopOwnedProcess)
	mux.HandleFunc("POST /api/processes/{id}/promote", s.ownerOnly(s.promoteProcess))
	mux.HandleFunc("GET /api/system/tools", s.ownerOnly(s.systemTools))
	// The machine registry (fleet/registry.go): every shell, tool and long-running process
	// agents started. ?project=<id> filters; IDs go in the body (they contain colons).
	mux.HandleFunc("GET /api/registry", s.registry)
	mux.HandleFunc("POST /api/registry/stop", s.registryStop)
	mux.HandleFunc("POST /api/registry/restart", s.registryRestart)
	mux.HandleFunc("GET /api/system/warnings", s.ownerOnly(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, s.systemWarnings) }))
}

func (s *Server) requestProcess(w http.ResponseWriter, r *http.Request) {
	var req fleet.ProcessRequest
	if !decode(w, r, &req) {
		return
	}
	c, _ := caller(r)
	ap, err := s.f.RequestProcess(c, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	status := http.StatusCreated
	if ap.Approval != nil {
		status = http.StatusAccepted
	}
	writeJSON(w, status, ap)
}
func (s *Server) agentProcesses(w http.ResponseWriter, r *http.Request) {
	c, _ := caller(r)
	p, err := s.f.AgentProcesses(c, r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
func (s *Server) startProcess(w http.ResponseWriter, r *http.Request) {
	var req fleet.ProcessRequest
	if !decode(w, r, &req) {
		return
	}
	if !s.RequireOwner(w, r, "start a command owned by this agent", req) {
		return
	}
	c, _ := caller(r)
	p, err := s.f.StartProcess(c, r.PathValue("id"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}
func (s *Server) stopOwnedProcess(w http.ResponseWriter, r *http.Request) {
	c, _ := caller(r)
	if err := s.f.StopOwnedProcess(c, r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
func (s *Server) promoteProcess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Project string `json:"project_id"`
		Owner   string `json:"owner_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !s.RequireOwner(w, r, "transfer this process to a project tool owner", req) {
		return
	}
	c, _ := caller(r)
	if err := s.f.PromoteProcess(c, r.PathValue("id"), req.Project, req.Owner); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
func (s *Server) registry(w http.ResponseWriter, r *http.Request) {
	c, _ := caller(r)
	list, err := s.f.Registry(c, r.URL.Query().Get("project"))
	if err != nil {
		writeErr(w, err)
		return
	}
	orphans := 0
	for _, e := range list {
		if e.Orphan {
			orphans++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": list, "orphans": orphans})
}

type registryRef struct {
	ID string `json:"id"`
}

func (s *Server) registryStop(w http.ResponseWriter, r *http.Request) {
	var req registryRef
	if !decode(w, r, &req) {
		return
	}
	c, _ := caller(r)
	if err := s.f.RegistryStop(c, req.ID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) registryRestart(w http.ResponseWriter, r *http.Request) {
	var req registryRef
	if !decode(w, r, &req) {
		return
	}
	c, _ := caller(r)
	e, err := s.f.RegistryRestart(c, req.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) systemTools(w http.ResponseWriter, r *http.Request) {
	p := s.f.SystemTools()
	if s.wa != nil {
		state := "disabled"
		if s.wa.Enabled() {
			state = string(s.wa.Status().State)
		}
		p = append(p, model.ProcessInfo{ID: "whatsapp", Category: model.CategorySystemTool, OwnerID: "system", Purpose: "WhatsApp watcher", Command: "built-in service", State: state})
	}
	writeJSON(w, http.StatusOK, p)
}
