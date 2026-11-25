package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

type sessionRow struct {
	Name           string    `json:"name"`
	Host           string    `json:"host"`
	ID             string    `json:"id"`
	Adapter        string    `json:"adapter"`
	State          string    `json:"state"`
	Title          string    `json:"title"`
	Role           string    `json:"role"`
	QueuedMessages int       `json:"queued_messages"`
	Managed        bool      `json:"managed"`
	Rooms          [0]string `json:"rooms"`
	Tier           string    `json:"tier"`
	Cwd            string    `json:"cwd"`
	Project        string    `json:"project"`
	ConversationID string    `json:"conversation_id"`
	StartedAt      float64   `json:"started_at"`
}

type sessionRoster struct {
	Host       string               `json:"host"`
	Commit     string               `json:"commit"`
	Version    string               `json:"version"`
	Daemon     string               `json:"daemon"`
	Sessions   []sessionRow         `json:"sessions"`
	Supervisor *peers.SupervisorRef `json:"supervisor"`
	System     peers.System         `json:"system"`
	NextOffset *int                 `json:"next_offset,omitempty"`
}

func recentCompanionSession(a model.Agent, cutoff int64) bool {
	if a.ShelvedAt != 0 || a.Desired == "stopped" || a.State == model.StateStopped {
		return false
	}
	switch a.State {
	case model.StateStarting, model.StateIdle, model.StateWorking, model.StateWaiting, model.StateQueued, model.StatePaused:
		return true
	default:
		return a.LastActivityAt >= cutoff
	}
}

func sessionFromAgent(a model.Agent, host, project string) sessionRow {
	return sessionRow{
		Name: a.Name, Host: host, ID: a.ID, Adapter: string(a.Harness), State: pythonState(a.State),
		Title: a.Role, Role: a.Role, QueuedMessages: a.Queued, Managed: true,
		Tier: string(a.Level), Cwd: a.Cwd, Project: project, ConversationID: a.SessionID,
		StartedAt: float64(a.CreatedAt) / 1000,
	}
}

func (s *Server) sessionRoster(compact bool) (sessionRoster, error) {
	snap, err := s.f.Snapshot()
	if err != nil {
		return sessionRoster{}, err
	}
	host := snap.Machine
	if s.peers != nil {
		host = s.peers.Machine()
	}
	projects := make(map[string]string, len(snap.Projects))
	for _, p := range snap.Projects {
		projects[p.ID] = p.Name
	}
	rows := make([]sessionRow, 0, len(snap.Agents))
	cutoff := time.Now().Add(-24 * time.Hour).UnixMilli()
	add := func(a model.Agent) {
		if compact && !recentCompanionSession(a, cutoff) {
			return
		}
		rows = append(rows, sessionFromAgent(a, host, projects[a.ProjectID]))
	}
	if snap.Head != nil {
		add(*snap.Head)
	}
	for _, a := range snap.Agents {
		if snap.Head == nil || a.ID != snap.Head.ID {
			add(a)
		}
	}
	return sessionRoster{Host: host, Commit: buildCommit(), Version: s.f.Version(), Daemon: peers.DaemonGo,
		Sessions: rows, Supervisor: s.supervisorRef(host), System: s.peerSystem()}, nil
}

func (roster *sessionRoster) page(r *http.Request) error {
	q := r.URL.Query()
	if q.Get("scope") != "all" && !q.Has("limit") && !q.Has("offset") {
		return nil
	}
	limit, offset := 100, 0
	var err error
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 200 {
			return fmt.Errorf("limit must be between 1 and 200")
		}
	}
	if q.Has("offset") {
		offset, err = strconv.Atoi(q.Get("offset"))
		if err != nil || offset < 0 {
			return fmt.Errorf("offset must be a nonnegative integer")
		}
	}
	offset = min(offset, len(roster.Sessions))
	end := min(offset+limit, len(roster.Sessions))
	if end < len(roster.Sessions) {
		roster.NextOffset = &end
	}
	roster.Sessions = roster.Sessions[offset:end]
	return nil
}

func wantsSessionStream(r *http.Request) bool {
	for _, value := range strings.Split(r.Header.Get("Accept"), ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err == nil && media == "text/event-stream" && params["q"] != "0" {
			return true
		}
	}
	return false
}

func (s *Server) companionSessionStream(w http.ResponseWriter, r *http.Request) {
	sub := s.f.Subscribe()
	defer s.f.Unsubscribe(sub)
	initial, err := s.sessionRoster(true)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	controller := http.NewResponseController(w)
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	send := func(kind string, value any) error {
		if !s.companion.LinkTokenMatches(strings.TrimSpace(token)) {
			return errors.New("Companion link token changed")
		}
		_ = controller.SetWriteDeadline(time.Now().Add(writeTimeout))
		if kind == "" {
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return err
			}
		} else {
			data, err := json.Marshal(value)
			if err != nil {
				return err
			}
			if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data); err != nil {
				return err
			}
		}
		return controller.Flush()
	}
	if send("snapshot", initial) != nil {
		return
	}
	previous := make(map[string]sessionRow, len(initial.Sessions))
	for _, row := range initial.Sessions {
		previous[row.ID] = row
	}
	upsert := func(row sessionRow) error {
		if previous[row.ID] == row {
			return nil
		}
		previous[row.ID] = row
		return send("upsert", row)
	}
	remove := func(id string) error {
		if _, exists := previous[id]; !exists {
			return nil
		}
		delete(previous, id)
		return send("remove", map[string]string{"id": id})
	}
	refresh := func() error {
		current, err := s.sessionRoster(true)
		if err != nil {
			return err
		}
		present := make(map[string]bool, len(current.Sessions))
		for _, row := range current.Sessions {
			present[row.ID] = true
			if err := upsert(row); err != nil {
				return err
			}
		}
		for id := range previous {
			if !present[id] {
				if err := remove(id); err != nil {
					return err
				}
			}
		}
		return nil
	}
	refreshAgent := func(id string) error {
		a, err := s.f.Agent(id)
		if err != nil || a.ID != id || !recentCompanionSession(a, time.Now().Add(-24*time.Hour).UnixMilli()) {
			return remove(id)
		}
		var project model.Project
		if a.ProjectID != "" {
			project, _ = s.f.Project(a.ProjectID)
		}
		return upsert(sessionFromAgent(a, initial.Host, project.Name))
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if refresh() != nil || send("", nil) != nil {
				return
			}
		case ev := <-sub.C():
			if sub.TakeResync() {
				// Recover dropped bus events by comparing the current roster
				// with what the client has already received.
				if refresh() != nil {
					return
				}
				continue
			}
			switch ev.Type {
			case model.EvAgentUpdated, model.EvAgentRemoved:
				if refreshAgent(ev.AgentID) != nil {
					return
				}
			case model.EvProjectUpdated, model.EvProjectRemoved:
				if refresh() != nil {
					return
				}
			}
		}
	}
}
