package api

import (
	"net/http"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/toolbox"
)

// The Resources section (docs/RESOURCES.md; package toolbox, because /api/resources is the
// machine load): one list of everything the agents can reach. Read only. The owner and
// every agent get the same view (the owner, 17 Nov: full agentic access); a record never holds
// a number, an address or a key, so there is nothing to hide from either.
func (s *Server) toolboxRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/toolbox", s.toolboxReaders(s.listToolbox))
	mux.HandleFunc("GET /api/toolbox/{id}", s.toolboxReaders(s.getToolbox))
}

// toolboxReaders: the owner, this node's agents and the fleet's other machines.
func (s *Server) toolboxReaders(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, _ := caller(r); !c.Owner && c.AgentID == "" && c.Peer == "" {
			writeError(w, http.StatusForbidden, "only the owner and agents read the resources")
			return
		}
		h(w, r)
	}
}

func (s *Server) listToolbox(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, state := q.Get("kind"), q.Get("state")
	if err := toolbox.CheckFilter(kind, state); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	list, live := s.toolboxList(r)
	writeJSON(w, http.StatusOK, toolbox.List{Machine: live.Machine, Resources: toolbox.Filter(list, kind, state), UpdatedAt: live.Now})
}

func (s *Server) getToolbox(w http.ResponseWriter, r *http.Request) {
	list, _ := s.toolboxList(r)
	res, ok := toolbox.Find(list, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such resource; GET /api/toolbox lists them")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// toolboxList is the declared list with what this node measures laid over it, sorted for
// reading.
func (s *Server) toolboxList(r *http.Request) ([]toolbox.Resource, toolbox.Live) {
	live := s.toolboxLive(r)
	list := toolbox.Apply(toolbox.Seed(), live)
	toolbox.Sort(list)
	return list, live
}

// agentUp: the agent has a process that can take mail (anything but stopped, exited, error).
func agentUp(st model.AgentState) bool {
	return st != model.StateStopped && st != model.StateExited && st != model.StateError
}

// toolboxLive gathers what this node knows right now. States, counts and labels only: never
// the WhatsApp number or an account's sign-in address.
func (s *Server) toolboxLive(r *http.Request) toolbox.Live {
	l := toolbox.Live{Machine: strings.ToLower(s.f.Machine()), Now: model.Now()}
	if s.peers != nil {
		l.Machine = s.peers.Machine() // the name the fleet's other machines know this one by
	}
	if snap, err := s.f.Snapshot(); err == nil {
		l.Agents = map[string]bool{}
		for _, a := range snap.Agents {
			l.Agents[a.Name] = agentUp(a.State)
		}
	}
	if s.peers != nil {
		l.Peers = map[string]toolbox.PeerLive{}
		for _, p := range s.peers.Statuses() {
			pl := toolbox.PeerLive{Online: p.OK, Agents: map[string]bool{}}
			for _, sess := range p.Sessions {
				pl.Agents[sess.Name] = sess.State != "exited" && sess.State != "stopped" && sess.State != "error"
			}
			l.Peers[p.Name] = pl
		}
	}
	for _, t := range s.f.SystemTools() {
		l.SystemTools = append(l.SystemTools, toolbox.SystemTool{ID: t.ID, Purpose: t.Purpose, State: t.State, Restarts: t.Restarts})
	}
	if s.wa != nil {
		w := &toolbox.WhatsAppLive{Enabled: s.wa.Enabled()}
		if w.Enabled {
			st := s.wa.Status()
			w.State, w.Error = string(st.State), st.Error
		}
		l.WhatsApp = w
	}
	if s.accounts != nil {
		al := &toolbox.AccountsLive{}
		for _, a := range s.accounts.Payload().Accounts {
			row := toolbox.AccountLive{Label: accountLabel(a.Label, a.Harness, a.Name), Installed: a.Installed, Connected: a.Connected}
			for _, lim := range a.Limits {
				if !row.HasLimit || lim.UsedPercent > row.UsedPercent {
					row.HasLimit, row.LimitLabel, row.UsedPercent = true, lim.Label, lim.UsedPercent
				}
			}
			if toolbox.SecretShape(row.LimitLabel) != "" {
				row.LimitLabel = ""
			}
			al.Accounts = append(al.Accounts, row)
		}
		l.Accounts = al
	}
	if s.companion != nil {
		st := s.companion.Status(r.Context(), false) // no probe: nothing leaves this node
		l.Companion = &toolbox.CompanionLive{Connected: st.Connected, LinkToken: st.LinkToken, LinkUsedAt: st.LinkUsedAt}
	}
	return l
}

// accountLabel names an account without its sign-in address: "Claude", "Claude (work)".
func accountLabel(label, harness, name string) string {
	if label == "" || toolbox.SecretShape(label) != "" {
		label = harness
	}
	if name != "" && name != "default" && toolbox.SecretShape(name) == "" {
		label += " (" + name + ")"
	}
	return label
}
