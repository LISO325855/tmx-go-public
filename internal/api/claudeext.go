package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Claude Code extensions (DESIGN.md "Profiles", Claude Code extensions): what Claude Code has
// installed, offered to profiles, and installing more through Claude Code itself. Owner only;
// installing asks the owner gate (it runs software from a marketplace or a command).
func (s *Server) claudeExtRoutes(mux *http.ServeMux) {
	o := func(h http.HandlerFunc) http.HandlerFunc { return s.ownerOnly(s.withClaudeExt(h)) }
	mux.HandleFunc("GET /api/claude/extensions", o(s.getClaudeExt))
	mux.HandleFunc("POST /api/claude/plugins", o(s.installClaudePlugin))
	mux.HandleFunc("POST /api/claude/mcp", o(s.addClaudeServer))
}

func (s *Server) withClaudeExt(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.claudeExt == nil {
			writeError(w, http.StatusNotImplemented, "Claude Code extensions are not available")
			return
		}
		h(w, r)
	}
}

// GET /api/claude/extensions[?fresh=1] → claudeext.Inventory (no config values).
func (s *Server) getClaudeExt(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.claudeExt.Inventory(r.Context(), r.URL.Query().Get("fresh") == "1"))
}

// POST /api/claude/plugins {"plugin": "name@marketplace"} → the fresh inventory.
func (s *Server) installClaudePlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Plugin string `json:"plugin"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Plugin = strings.TrimSpace(req.Plugin)
	if req.Plugin == "" {
		writeError(w, http.StatusBadRequest, "which plugin?")
		return
	}
	if !s.RequireOwner(w, r, "install the Claude Code plugin "+short(req.Plugin, 80), req) {
		return
	}
	if err := s.claudeExt.InstallPlugin(r.Context(), req.Plugin); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.claudeExt.Inventory(r.Context(), true))
}

// POST /api/claude/mcp {"name", "config": {...}} adds a user-scope MCP server to Claude Code
// (claude mcp add-json -s user) → the fresh inventory.
func (s *Server) addClaudeServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Config) == 0 {
		writeError(w, http.StatusBadRequest, "the server needs a name and a config")
		return
	}
	if !s.RequireOwner(w, r, "add the MCP server "+short(req.Name, 60)+" to Claude Code", req) {
		return
	}
	if err := s.claudeExt.AddServer(r.Context(), req.Name, req.Config); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.claudeExt.Inventory(r.Context(), true))
}
