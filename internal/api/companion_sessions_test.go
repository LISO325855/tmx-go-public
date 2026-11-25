package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api"
	tc "github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/companion"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/peers"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

func TestCompanionSessionsSnapshotAndDeltas(t *testing.T) {
	paths := config.Paths{Home: t.TempDir()}
	st, err := store.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now()
	old := now.Add(-7 * 24 * time.Hour).UnixMilli()
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("agent-%04d", i)
		a := model.Agent{ID: name, Name: name, Level: model.LevelWorker, State: model.StateStopped, Desired: "stopped",
			CreatedAt: old, LastActivityAt: old, UpdatedAt: old}
		switch {
		case i == 0:
			a.Name, a.Level, a.State, a.Desired = "head", model.LevelHead, model.StateIdle, "running"
		case i < 49:
			a.State, a.Desired = model.StateIdle, "running"
		case i == 49:
			a.State, a.Desired, a.LastActivityAt = model.StateError, "running", now.Add(-23*time.Hour).UnixMilli()
		case i == 999:
			a.ShelvedAt = now.UnixMilli()
		}
		if err := st.PutAgent(a); err != nil {
			t.Fatal(err)
		}
	}
	f, err := fleet.New(fleet.Options{Store: st, Paths: paths, Machine: "testbox", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close(context.Background()) })
	c := companion.New(companion.Options{Store: st, Paths: paths, Fleet: f})
	t.Cleanup(c.Close)
	linkToken, err := config.CompanionToken(paths, true)
	if err != nil {
		t.Fatal(err)
	}
	ownerToken, err := config.OwnerToken(paths)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.New(api.Options{Fleet: f, Companion: c, OwnerToken: ownerToken}).Handler())
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type roster struct {
		Sessions   []peers.Session `json:"sessions"`
		NextOffset *int            `json:"next_offset"`
	}
	read := func(token, query string) roster {
		t.Helper()
		var value roster
		if err := tc.New(server.URL, token).Get(ctx, "/api/sessions"+query, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := read(linkToken, ""); len(got.Sessions) != 50 || got.NextOffset != nil {
		t.Fatalf("compact roster: %d agents, next %v", len(got.Sessions), got.NextOffset)
	}
	if got := read(ownerToken, ""); len(got.Sessions) != 1000 || got.NextOffset != nil {
		t.Fatalf("owner roster: %d agents, next %v", len(got.Sessions), got.NextOffset)
	}
	if got := read(linkToken, "?scope=all"); len(got.Sessions) != 100 || got.NextOffset == nil || *got.NextOffset != 100 {
		t.Fatalf("archive first page: %d agents, next %v", len(got.Sessions), got.NextOffset)
	}
	if got := read(linkToken, "?scope=all&offset=950"); len(got.Sessions) != 50 || got.NextOffset != nil || got.Sessions[49].ID != "agent-0999" {
		t.Fatalf("archive final page: %d agents, next %v", len(got.Sessions), got.NextOffset)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+linkToken)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	next := func(kind string, target any) {
		t.Helper()
		lines := []string{}
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if len(lines) != 0 {
					break
				}
				continue
			}
			if !strings.HasPrefix(line, ":") {
				lines = append(lines, line)
			}
		}
		if len(lines) != 2 || lines[0] != "event: "+kind || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("expected %s event, got %q, read error %v", kind, lines, scanner.Err())
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), target); err != nil {
			t.Fatal(err)
		}
	}
	var initial roster
	next("snapshot", &initial)

	// Model Companion's 500-slot queue, even if it expands the initial snapshot
	// to one event per session before consuming any of them.
	subscriber := make(chan peers.Session, 500)
	receive := func(row peers.Session) {
		t.Helper()
		select {
		case subscriber <- row:
		default:
			t.Fatalf("subscriber overflow at %s", row.ID)
		}
	}
	for _, row := range initial.Sessions {
		receive(row)
	}
	if len(subscriber) != 50 {
		t.Fatalf("snapshot enqueued %d events", len(subscriber))
	}

	// A burst of old-agent notifications must not leak into the compact feed,
	// including if the underlying Go bus needs to resynchronize.
	for i := 50; i < 1000; i++ {
		f.Publish(model.BusEvent{Type: model.EvAgentUpdated, AgentID: fmt.Sprintf("agent-%04d", i)})
	}
	role := "Updated role"
	if _, err := f.UpdateAgent(fleet.OwnerCaller, "head", fleet.AgentPatch{Role: &role}); err != nil {
		t.Fatal(err)
	}
	var updated peers.Session
	next("upsert", &updated)
	receive(updated)
	if updated.Name != "head" || updated.Role != role || len(subscriber) != 51 {
		t.Fatalf("change did not produce one row: %+v, queue length %d", updated, len(subscriber))
	}
	if err := f.RemoveAgent(fleet.OwnerCaller, "agent-0001"); err != nil {
		t.Fatal(err)
	}
	var removed struct {
		ID string `json:"id"`
	}
	next("remove", &removed)
	if removed.ID != "agent-0001" {
		t.Fatalf("removed %q", removed.ID)
	}
}
