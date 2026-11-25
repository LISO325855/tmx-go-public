package companion

import (
	"encoding/json"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func pollingFixture(t *testing.T, handler http.HandlerFunc) (*Service, model.CompanionTask) {
	t.Helper()
	remote := httptest.NewServer(handler)
	t.Cleanup(remote.Close)
	paths := config.Paths{Home: t.TempDir()}
	if err := config.Save(paths, config.Config{Companion: &config.CompanionConfig{URL: remote.URL}}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := New(Options{Store: st, Paths: paths, Fleet: answerPollFleet{}, Poll: time.Millisecond, RetryMin: time.Millisecond})
	t.Cleanup(s.Close)
	task := model.CompanionTask{ID: "local", RemoteID: "remote", Kind: model.CompanionKindMessage, Origin: "owner", State: model.CompanionWorking, SentAt: model.Now(), CreatedAt: model.Now()}
	if err := st.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	return s, task
}

func TestSlowPollBacksOff(t *testing.T) {
	s, task := pollingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(Task{ID: "remote"})
	})
	if delay := s.poll(task); delay < 60*time.Millisecond || delay > time.Minute {
		t.Fatalf("delay %s", delay)
	}
}

func TestCancelRetries503AndKeepsFailureOnTask(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovers", true: "unconfirmed"}[fail], func(t *testing.T) {
			var attempts atomic.Int32
			s, task := pollingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && (attempts.Add(1) < 3 || fail) {
					http.Error(w, "busy", 503)
					return
				}
				_ = json.NewEncoder(w).Encode(Task{ID: "remote"})
			})
			got, err := s.Cancel(task.ID, "")
			if attempts.Load() != 3 {
				t.Fatalf("attempts %d", attempts.Load())
			}
			if fail {
				if err == nil || got.State != model.CompanionWorking || !strings.Contains(got.Error, "Cancellation not confirmed") {
					t.Fatalf("failure: %+v %v", got, err)
				}
				s.poll(got)
				got, _ = s.Get(task.ID)
				if !strings.Contains(got.Error, "Cancellation not confirmed") {
					t.Fatal("poll erased cancellation failure")
				}
			} else if err != nil || got.State != model.CompanionCanceled {
				t.Fatalf("success: %+v %v", got, err)
			}
		})
	}
}

func TestPollPacing(t *testing.T) {
	s := New(Options{Poll: 3 * time.Second})
	defer s.Close()
	task := model.CompanionTask{ID: "a", State: model.CompanionWorking}
	if got := s.nextPoll(task, time.Millisecond, nil); got != 3*time.Second {
		t.Fatalf("fresh: %s", got)
	}
	task.SentAt = model.Now() - (20 * time.Minute).Milliseconds()
	if got := s.nextPoll(task, time.Millisecond, nil); got != 30*time.Second {
		t.Fatalf("aged: %s", got)
	}
	task.SentAt = 0
	task.State = model.CompanionInputRequired
	if got := s.nextPoll(task, time.Millisecond, nil); got != 9*time.Second {
		t.Fatalf("question: %s", got)
	}
	task.State = model.CompanionWorking
	busy := &Error{Status: 503}
	for _, want := range []time.Duration{6, 12, 24, 48, 96, 120, 120} {
		if got := s.nextPoll(task, time.Millisecond, busy); got != want*time.Second {
			t.Fatalf("backoff: %s want %s", got, want*time.Second)
		}
	}
	if got := s.nextPoll(task, time.Millisecond, nil); got != 3*time.Second {
		t.Fatalf("reset: %s", got)
	}
	if got := s.nextPoll(task, 3*time.Second, nil); got != 15*time.Second {
		t.Fatalf("slow: %s", got)
	}
	if got := s.nextPoll(task, time.Millisecond, &Error{Status: 503, RetryAfter: 5 * time.Minute}); got != 5*time.Minute {
		t.Fatalf("retry after: %s", got)
	}
	task.ID = "b"
	if got := s.nextPoll(task, time.Millisecond, busy); got != 6*time.Second {
		t.Fatalf("new task: %s", got)
	}
}

func TestPollUsesRetryAfter(t *testing.T) {
	s, task := pollingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "45")
		http.Error(w, "busy", 503)
	})
	if got := s.poll(task); got < 45*time.Second {
		t.Fatalf("poll ignored Retry-After: %s", got)
	}
}
