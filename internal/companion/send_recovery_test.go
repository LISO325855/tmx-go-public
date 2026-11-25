package companion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

func TestSendRecovery(t *testing.T) {
	for _, mode := range []string{"created", "delayed-created", "before-created", "timeout-created", "lookup-fails", "exhausted", "busy"} {
		t.Run(mode, func(t *testing.T) {
			var posts, gets atomic.Int32
			remote := Task{ID: "existing", History: []Message{{Role: "ROLE_USER", Parts: []Part{{Text: "exact text"}}}}}
			remote.Status.State = StateWorking
			remote.CreateTime = time.Now().UTC().Format(time.RFC3339Nano)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					if mode == "busy" {
						w.WriteHeader(409)
						_, _ = w.Write([]byte(`{"error":{"message":"busy","details":[{"reason":"CONVERSATION_BUSY"}]}}`))
						return
					}
					if mode == "timeout-created" {
						select {
						case <-r.Context().Done():
						case <-time.After(100 * time.Millisecond):
						}
						return
					}
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{"detail":"brownout"}`))
					return
				}
				n := gets.Add(1)
				if mode == "lookup-fails" {
					w.WriteHeader(503)
					return
				}
				tasks := []Task{}
				if mode == "created" || mode == "timeout-created" || mode == "delayed-created" && n >= 3 {
					tasks = append(tasks, remote)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"tasks": tasks})
			}))
			defer server.Close()
			timeout := time.Second
			if mode == "timeout-created" {
				timeout = 20 * time.Millisecond
			}
			client := &Client{BaseURL: server.URL, HTTP: &http.Client{Timeout: timeout}}
			start := time.Now()
			rt, err := client.sendRecover(context.Background(), "exact text", sendRecoveryPolicy{window: 40 * time.Millisecond, interval: 5 * time.Millisecond}, func(int, string) bool { return true })
			wantPosts := int32(1)
			if posts.Load() != wantPosts {
				t.Fatalf("posts = %d, want %d", posts.Load(), wantPosts)
			}
			switch mode {
			case "busy":
				if e := AsError(err); e == nil || e.Reason != ReasonBusy || gets.Load() != 0 {
					t.Fatalf("busy: %v, lookups %d", err, gets.Load())
				}
			case "lookup-fails", "exhausted", "before-created":
				if e := AsError(err); e == nil || !e.Unconfirmed || e.Unreachable {
					t.Fatalf("must remain unconfirmed: %v", err)
				}
			default:
				if err != nil || rt.ID != remote.ID {
					t.Fatalf("task %+v, err %v", rt, err)
				}
			}
			if mode == "before-created" && time.Since(start) < 40*time.Millisecond {
				t.Fatal("resent before observation window")
			}
		})
	}
}

func TestSendSlowHeaders(t *testing.T) {
	if testing.Short() {
		t.Skip("real 60-second header delay")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timer := time.NewTimer(60 * time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		_, _ = w.Write([]byte(`{"task":{"id":"slow","status":{"state":"TASK_STATE_WORKING"}}}`))
	}))
	defer server.Close()
	c := &Client{BaseURL: server.URL}
	rt, err := c.Send(context.Background(), "slow headers", "", "")
	if err != nil || rt.ID != "slow" {
		t.Fatalf("task %+v, error %v", rt, err)
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, value := range []string{"3", now.Add(3 * time.Second).Format(http.TimeFormat)} {
		if got := retryAfter(value, now); got != 3*time.Second {
			t.Fatalf("%q: %s", value, got)
		}
	}
	for _, value := range []string{"", "-1", "garbage", "99999999999999999999999999"} {
		if got := retryAfter(value, now); got != 0 {
			t.Fatalf("%q: %s", value, got)
		}
	}
}

func TestBusySendWaitsInQueue(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":{"message":"busy","details":[{"reason":"CONVERSATION_BUSY"}]}}`))
	}))
	defer server.Close()
	s, st := recoveryService(t, server.URL)
	task := model.CompanionTask{ID: "queued", Origin: "owner", State: model.CompanionQueued, Text: "hello", CreatedAt: model.Now()}
	if err := st.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	s.step()
	current, _ := st.GetCompanionTask(task.ID)
	if current.State != model.CompanionQueued || current.NextAt < model.Now()+900 {
		t.Fatalf("did not wait: %+v", current)
	}
	s.step()
	if posts.Load() != 1 {
		t.Fatalf("busy immediately retried: %d", posts.Load())
	}
	time.Sleep(time.Until(time.UnixMilli(current.NextAt)) + time.Millisecond)
	s.step()
	if posts.Load() != 2 {
		t.Fatalf("busy did not retry later: %d", posts.Load())
	}
}

func TestIdleRecoveryWakesAndFindsTask(t *testing.T) {
	var visible atomic.Bool
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		tasks := []Task{}
		if visible.Load() {
			rt := Task{ID: "remote", History: []Message{{Role: "ROLE_USER", Parts: []Part{{Text: "hello"}}}}}
			rt.Status.State = StateWorking
			rt.CreateTime = time.Now().UTC().Format(time.RFC3339Nano)
			tasks = append(tasks, rt)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": tasks})
	}))
	defer server.Close()
	s, st := recoveryService(t, server.URL)
	task := model.CompanionTask{ID: "unconfirmed", Origin: "owner", State: model.CompanionUnconfirmed, Text: "hello", CreatedAt: model.Now(), SentAt: model.Now()}
	if err := st.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	if delay := s.step(); delay > 3*time.Second {
		t.Fatalf("idle recovery sleeps %s", delay)
	}
	if delay := s.step(); delay > 3*time.Second || gets.Load() != 1 {
		t.Fatalf("throttle delay %s, gets %d", delay, gets.Load())
	}
	visible.Store(true)
	s.lastRecover = time.Time{}
	if !s.recoverUnconfirmed() {
		t.Fatal("did not recover")
	}
	current, _ := st.GetCompanionTask(task.ID)
	if current.RemoteID != "remote" || current.State != model.CompanionWorking {
		t.Fatalf("not recovered: %+v", current)
	}
}

func recoveryService(t *testing.T, url string) (*Service, *store.Store) {
	t.Helper()
	paths := config.Paths{Home: t.TempDir()}
	if err := config.Save(paths, config.Config{Companion: &config.CompanionConfig{URL: url}}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteSecret(paths.CompanionAgentTokenFile(), "test-key"); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := New(Options{Store: st, Fleet: answerPollFleet{}, Paths: paths, HTTP: &http.Client{}, RetryMin: time.Millisecond})
	t.Cleanup(s.Close)
	return s, st
}

func TestSendRecoveryNeverResendsRetryAfter(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"tasks":[]}`))
			return
		}
		posts.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(503)
	}))
	defer server.Close()
	c := &Client{BaseURL: server.URL}
	_, err := c.sendRecover(context.Background(), "hello", sendRecoveryPolicy{window: 20 * time.Millisecond, interval: time.Millisecond}, func(int, string) bool { return true })
	if e := AsError(err); e == nil || !e.Unconfirmed || posts.Load() != 1 {
		t.Fatalf("posts %d, err %v", posts.Load(), err)
	}
}

func TestRecoveryRejectsOldAndBoundTasks(t *testing.T) {
	now := time.Now()
	old := Task{ID: "old", CreateTime: now.Add(-5 * time.Minute).Format(time.RFC3339Nano), History: []Message{{Role: "ROLE_USER", Parts: []Part{{Text: "hello"}}}}}
	fresh := old
	fresh.ID, fresh.CreateTime = "new", now.Format(time.RFC3339Nano)
	bound := fresh
	bound.ID = "bound"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": []Task{old, bound, fresh}})
	}))
	defer server.Close()
	s, st := recoveryService(t, server.URL)
	if err := st.PutCompanionTask(model.CompanionTask{ID: "previous", RemoteID: "bound", State: model.CompanionCompleted}); err != nil {
		t.Fatal(err)
	}
	c := &Client{BaseURL: server.URL}
	rt, err := c.sendRecover(context.Background(), "hello", sendRecoveryPolicy{window: time.Millisecond, interval: time.Millisecond, firstAttempt: now, eligible: func(rt Task) bool { return s.remoteAvailable("current", rt.ID) }}, func(int, string) bool { return true })
	if err != nil || rt.ID != "new" {
		t.Fatalf("got %s, %v", rt.ID, err)
	}
	old.Status.Timestamp = now.Format(time.RFC3339Nano)
	if recoveryMatch(old, "hello", "", now) {
		t.Fatal("old task matched by recent status update")
	}
	fresh.CreateTime = ""
	if recoveryMatch(fresh, "hello", "", now) {
		t.Fatal("missing creation time matched by text")
	}
	fresh.History = []Message{{Role: "ROLE_USER", MessageID: "sent-id"}}
	if !recoveryMatch(fresh, "hello", "sent-id", now) {
		t.Fatal("original message ID not matched")
	}
}

func TestRecoveryExpiresWithoutResend(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	s, st := recoveryService(t, server.URL)
	task := model.CompanionTask{ID: "expired", Kind: model.CompanionKindMessage, Origin: "owner", State: model.CompanionUnconfirmed, SentAt: time.Now().Add(-16 * time.Minute).UnixMilli()}
	if err := st.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	s.recoverUnconfirmed()
	got, _ := st.GetCompanionTask(task.ID)
	if got.Error != notConfirmedNote || requests.Load() != 0 {
		t.Fatalf("got %+v, requests %d", got, requests.Load())
	}
}

func TestRecoveryPrefersMessageIdentity(t *testing.T) {
	now := time.Now()
	textMatch := Task{ID: "text-only", CreateTime: now.Format(time.RFC3339Nano), History: []Message{{Role: "ROLE_USER", Parts: []Part{{Text: "hello"}}}}}
	identityMatch := Task{ID: "identity", History: []Message{{Role: "ROLE_USER", MessageID: "sent-id", Parts: []Part{{Text: "hello"}}}}}
	rt, ok := findRecoveryTask([]Task{textMatch, identityMatch}, "hello", sendRecoveryPolicy{firstAttempt: now, messageID: "sent-id"})
	if !ok || rt.ID != "identity" {
		t.Fatalf("got %s, %v", rt.ID, ok)
	}
}

func TestBackgroundRecoveryUsesThirtySecondCadence(t *testing.T) {
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gets.Add(1); _, _ = w.Write([]byte(`{"tasks":[]}`)) }))
	defer server.Close()
	s, st := recoveryService(t, server.URL)
	task := model.CompanionTask{ID: "waiting", State: model.CompanionUnconfirmed, SentAt: time.Now().Add(-2 * time.Minute).UnixMilli()}
	if err := st.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	s.lastRecover = time.Now().Add(-10 * time.Second)
	if !s.recoverUnconfirmed() || gets.Load() != 0 {
		t.Fatal("queried before 30 seconds")
	}
	s.lastRecover = time.Now().Add(-31 * time.Second)
	if !s.recoverUnconfirmed() || gets.Load() != 1 {
		t.Fatal("did not query after 30 seconds")
	}
}

func TestRecoveryMetadataCreationTime(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, top, meta string
		want            bool
	}{
		{"metadata", "", now.Format(time.RFC3339Nano), true},
		{"legacy", now.Format(time.RFC3339Nano), "", true},
		{"old metadata", "", now.Add(-time.Minute).Format(time.RFC3339Nano), false},
		{"missing", "", "", false},
		{"invalid", "", "invalid", false},
		{"conflicting", now.Add(-time.Minute).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, _ := json.Marshal(map[string]any{"id": "remote", "createTime": tc.top, "metadata": map[string]string{"createTime": tc.meta}, "history": []Message{{Role: "ROLE_USER", Parts: []Part{{Text: "same"}}}}})
			var task Task
			if err := json.Unmarshal(wire, &task); err != nil {
				t.Fatal(err)
			}
			if got := recoveryMatch(task, "same", "sent-id", now); got != tc.want {
				t.Fatalf("match=%v", got)
			}
		})
	}
	// A text match appearing first must not defeat the later exact identity.
	text := Task{ID: "text", History: []Message{{Role: "ROLE_USER", Parts: []Part{{Text: "same"}}}}}
	text.Metadata.CreateTime = now.Format(time.RFC3339Nano)
	exact := text
	exact.ID = "exact"
	exact.History = []Message{{Role: "ROLE_USER", MessageID: "sent-id", Parts: []Part{{Text: "same"}}}}
	got, ok := findRecoveryTask([]Task{text, exact}, "same", sendRecoveryPolicy{firstAttempt: now, messageID: "sent-id"})
	if !ok || got.ID != "exact" {
		t.Fatalf("identity lost: %+v", got)
	}
	exact.Metadata.CreateTime = now.Add(-time.Minute).Format(time.RFC3339Nano)
	if recoveryMatch(exact, "same", "sent-id", now) {
		t.Fatal("identity overrode old creation time")
	}
}
