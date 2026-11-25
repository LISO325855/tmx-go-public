package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/gate"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/rules"
)

// Fleet rules (DESIGN.md "Fleet rules"): where this node's standing rules come from, which
// commit is in force, the documents, and the owner's settings of the source.

func (s *Server) rulesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/rules", s.fleetCaller(s.getRules))
	mux.HandleFunc("GET /api/rules/doc", s.fleetCaller(s.getRulesDoc))
	mux.HandleFunc("GET /api/rules/apps", s.fleetCaller(s.getRulesApps))
	mux.HandleFunc("POST /api/rules/read", s.fleetCaller(s.postRulesRead))
	mux.HandleFunc("POST /api/rules/refresh", s.fleetCaller(s.postRulesRefresh))
	mux.HandleFunc("PUT /api/rules/config", s.ownerOnly(s.putRulesConfig))
}

// fleetCaller wraps a handler for the owner and this machine's agents: the rules name people
// and apps, so Companion and other machines do not read them here.
func (s *Server) fleetCaller(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, ok := caller(r); !ok || c.Companion || c.Peer != "" || (!c.Owner && c.AgentID == "") {
			writeError(w, http.StatusForbidden, "only the owner and this machine's agents read the fleet rules")
			return
		}
		h(w, r)
	}
}

// RulesAgent is an agent with the commit its instructions were built from.
type RulesAgent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Commit string `json:"commit,omitempty"` // "" = built before rules were loaded
	Behind bool   `json:"behind"`           // running on other rules than those in force
}

// RulesView is GET /api/rules.
type RulesView struct {
	rules.Status
	Agents []RulesAgent `json:"agents"`
}

func (s *Server) rulesView() RulesView {
	v := RulesView{Status: s.f.Rules().Status(), Agents: []RulesAgent{}}
	cur := ""
	if v.Head != nil {
		cur = v.Head.Commit
	}
	starts := s.f.RulesStarts()
	snap, _ := s.f.Snapshot()
	for _, a := range snap.Agents {
		if a.Harness == model.HarnessShell {
			continue
		}
		commit := starts[a.ID]
		v.Agents = append(v.Agents, RulesAgent{ID: a.ID, Name: a.Name, State: string(a.State), Commit: commit,
			Behind: v.Loaded && a.State != model.StateStopped && commit != cur})
	}
	sort.Slice(v.Agents, func(i, j int) bool { return v.Agents[i].Name < v.Agents[j].Name })
	return v
}

// GET /api/rules: the source, the commit in force, the documents (without their text) and
// which agents run on older rules.
func (s *Server) getRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.rulesView())
}

// RulesDoc is one document with its text.
type RulesDoc struct {
	rules.Doc
	Body string `json:"body"`
}

// GET /api/rules/doc?name=pages/operations: one document of the commit in force.
func (s *Server) getRulesDoc(w http.ResponseWriter, r *http.Request) {
	d, body, ok := s.f.Rules().Doc(r.URL.Query().Get("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "there is no such document in the fleet rules")
		return
	}
	writeJSON(w, http.StatusOK, RulesDoc{Doc: d, Body: body})
}

// GET /api/rules/apps: the connected apps of the source (apps/*.md), each with the fields of
// its card (the front matter) and its guide.
func (s *Server) getRulesApps(w http.ResponseWriter, r *http.Request) {
	out := []RulesDoc{}
	for _, d := range s.f.Rules().DocsUnder("apps") {
		if _, body, ok := s.f.Rules().Doc(d.Name); ok {
			out = append(out, RulesDoc{Doc: d, Body: body})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/rules/read {name, log, refresh}: what rules_read answers the caller (an agent
// gets its own profile's view and what changed since its instructions were built).
func (s *Server) postRulesRead(w http.ResponseWriter, r *http.Request) {
	var req fleet.RulesReadRequest
	if !decode(w, r, &req) {
		return
	}
	c, _ := caller(r)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	out, err := s.f.RulesRead(ctx, c, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": out})
}

// POST /api/rules/refresh: look at the source now (the owner or the head). The answer is the
// view after the look; a look that brought nothing new says why in "problem".
func (s *Server) postRulesRefresh(w http.ResponseWriter, r *http.Request) {
	c, _ := caller(r)
	if !c.Owner && c.AgentID != s.f.HeadID() {
		writeError(w, http.StatusForbidden, "only the owner or the head starts a look at the rules source")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	_ = s.f.Rules().Refresh(ctx)
	writeJSON(w, http.StatusOK, s.rulesView())
}

// RulesConfigRequest is PUT /api/rules/config: the fields given replace the saved ones.
type RulesConfigRequest struct {
	Source       *string               `json:"source,omitempty"`
	Branch       *string               `json:"branch,omitempty"`
	Pin          *string               `json:"pin,omitempty"`
	Signers      *[]config.RulesSigner `json:"signers,omitempty"`
	FetchMinutes *int                  `json:"fetch_minutes,omitempty"`
	Off          *bool                 `json:"off,omitempty"`
}

// PUT /api/rules/config: the owner's settings of the rules source. Changing where the rules
// come from or which keys sign them always needs a fresh proof of the owner on this machine,
// also while the gate is off, and a daemon without a gate refuses it: whoever sets the source
// or pins a key writes every agent's instructions. Holding the node at a commit, the branch,
// the interval and turning the source off go through the gate as other settings do.
func (s *Server) putRulesConfig(w http.ResponseWriter, r *http.Request) {
	var req RulesConfigRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := config.Load(s.f.Paths())
	if err != nil {
		writeErr(w, err)
		return
	}
	next := config.RulesConfig{}
	if c.Rules != nil {
		next = *c.Rules
	}
	var changes []string
	trust := false
	if req.Source != nil && strings.TrimSpace(*req.Source) != next.Source {
		next.Source = strings.TrimSpace(*req.Source)
		if next.Source != "" {
			if err := rules.CheckSource(next.Source); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		changes, trust = append(changes, "read the fleet rules from "+orDefault(next.Source, "the built-in source")), true
	}
	if req.Signers != nil {
		if _, err := rules.ParseSigners(*req.Signers); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var ids []string
		for _, sg := range *req.Signers {
			ids = append(ids, sg.ID+" "+rules.SignerFingerprint(sg))
		}
		next.Signers = append([]config.RulesSigner(nil), (*req.Signers)...)
		changes, trust = append(changes, "pin the keys that sign the fleet rules: "+orDefault(strings.Join(ids, ", "), "none (commits are then taken unsigned)")), true
	}
	if req.Branch != nil && strings.TrimSpace(*req.Branch) != next.Branch {
		next.Branch = strings.TrimSpace(*req.Branch)
		if next.Branch != "" {
			if err := rules.CheckBranch(next.Branch); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		changes = append(changes, "follow the branch "+orDefault(next.Branch, config.DefaultRulesBranch)+" of the fleet rules")
	}
	if req.Pin != nil && strings.ToLower(strings.TrimSpace(*req.Pin)) != next.Pin {
		next.Pin = strings.ToLower(strings.TrimSpace(*req.Pin))
		if next.Pin != "" && !rules.IsCommit(next.Pin) {
			writeError(w, http.StatusBadRequest, "the commit to hold this node at must be a full commit id (40 hex digits)")
			return
		}
		if next.Pin == "" {
			changes = append(changes, "stop holding this node at one commit of the fleet rules")
		} else {
			changes = append(changes, "hold this node at commit "+next.Pin[:12]+" of the fleet rules")
		}
	}
	if req.FetchMinutes != nil && *req.FetchMinutes != next.FetchMinutes {
		if *req.FetchMinutes < 0 || *req.FetchMinutes > 24*60 {
			writeError(w, http.StatusBadRequest, "the interval must be between 1 minute and a day (0 = the default)")
			return
		}
		next.FetchMinutes = *req.FetchMinutes
		changes = append(changes, "look at the fleet rules source at another interval")
	}
	if req.Off != nil && *req.Off != next.Off {
		next.Off = *req.Off
		if next.Off {
			changes = append(changes, "turn the fleet rules off on this node")
		} else {
			changes = append(changes, "turn the fleet rules on on this node")
		}
	}
	if len(changes) == 0 {
		writeJSON(w, http.StatusOK, s.rulesView())
		return
	}
	a := gate.Action{Reason: strings.Join(changes, "; "), Key: actionKey(r, req)}
	if trust {
		a.Always, a.Fresh = true, true
	}
	if !s.requireOwner(w, r, a) {
		return
	}
	c.Rules = &next
	if err := config.Save(s.f.Paths(), c); err != nil {
		writeErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	_ = s.f.Rules().Refresh(ctx) // the settings apply at a look; what it found is in the view
	writeJSON(w, http.StatusOK, s.rulesView())
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
