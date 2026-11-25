package companion

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

type taskTransport func(*http.Request) (*http.Response, error)

func (f taskTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type taskMail struct {
	answerPollFleet
	agent string
	mail  []string
}

func (f *taskMail) DeliverCompanion(agent, body string) (model.Message, error) {
	f.agent = agent
	f.mail = append(f.mail, body)
	return model.Message{}, nil
}

func taskEndpoint(t *testing.T, endpoint taskTransport) (*Service, model.CompanionTask, *taskMail) {
	t.Helper()
	p := config.Paths{Home: t.TempDir()}
	if err := config.Save(p, config.Config{Companion: &config.CompanionConfig{URL: "http://127.0.0.1:8010"}}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(p.DB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fleet := &taskMail{}
	s := New(Options{Store: st, Paths: p, Fleet: fleet, HTTP: &http.Client{Transport: endpoint}})
	t.Cleanup(s.Close)
	task := model.CompanionTask{ID: "local", RemoteID: "remote", AgentID: "asker", Origin: "agent",
		State: model.CompanionWorking, SentAt: model.Now() - (40 * time.Minute).Milliseconds(), PollStoppedAt: model.Now()}
	if err := st.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	return s, task, fleet
}

func taskResponse(state, result string) *http.Response {
	w := httptest.NewRecorder()
	rt := Task{ID: "remote"}
	rt.Status.State = state
	if result != "" {
		rt.History = []Message{{Role: "ROLE_AGENT", Parts: []Part{{Text: result}}}}
	}
	_ = json.NewEncoder(w).Encode(rt)
	return w.Result()
}

func TestTaskResolvesOnFirstReadAfterDroppedConnection(t *testing.T) {
	reads := 0
	s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/a2a/v1/tasks/remote" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		reads++
		if reads == 1 || reads == 2 {
			// The run has ended, but tmx loses the response during the link interruption.
			return nil, io.ErrUnexpectedEOF
		}
		return taskResponse(StateCompleted, "The run report."), nil
	})
	s.step()
	unknown, _ := s.Get(task.ID)
	if unknown.State != model.CompanionUnknown || !strings.HasPrefix(unknown.Error, "Unknown: link down since ") {
		t.Fatalf("after interruption: %+v", unknown)
	}
	s.poll(unknown)
	stillUnknown, _ := s.Get(task.ID)
	if stillUnknown.Error != unknown.Error {
		t.Fatalf("outage start changed: %q -> %q", unknown.Error, stillUnknown.Error)
	}

	// Simulate restarting while the link is down with the old persisted stop marker.
	restarted := New(s.o)
	defer restarted.Close()
	restarted.step()
	got, _ := restarted.Get(task.ID)
	if reads != 3 || got.State != model.CompanionCompleted || got.PollStoppedAt != 0 || got.Result != "The run report." || got.Error != "" {
		t.Fatalf("after first successful read: %+v, reads=%d", got, reads)
	}
	restarted.step()
	if fleet.agent != task.AgentID || len(fleet.mail) != 1 || !strings.Contains(fleet.mail[0], got.Result) {
		t.Fatalf("outcome delivery: %s %q", fleet.agent, fleet.mail)
	}
}

func TestTaskEndedWithoutReport(t *testing.T) {
	for _, gone := range []bool{true, false} {
		name := "empty result"
		if gone {
			name = "gone"
		}
		t.Run(name, func(t *testing.T) {
			s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
				if gone {
					w := httptest.NewRecorder()
					w.WriteHeader(http.StatusNotFound)
					return w.Result(), nil
				}
				return taskResponse(StateCompleted, ""), nil
			})
			task.State = model.CompanionInputRequired
			if err := s.o.Store.PutCompanionTask(task); err != nil {
				t.Fatal(err)
			}
			s.step()
			got, _ := s.Get(task.ID)
			if got.State != model.CompanionEndedWithoutReport || got.FinishedAt == 0 || fleet.agent != task.AgentID ||
				len(fleet.mail) != 1 || !strings.Contains(fleet.mail[0], "The Companion task ended without a report.") {
				t.Fatalf("outcome: %+v, mail %q", got, fleet.mail)
			}
			if gone && (!strings.Contains(got.Error, "key in Companion replaced?") ||
				!strings.Contains(fleet.mail[0], "key in Companion replaced?")) {
				t.Fatalf("missing 404 diagnosis: %+v, mail %q", got, fleet.mail)
			}
		})
	}
}

func TestCancelAlreadyEndedTask(t *testing.T) {
	for _, endsDuringCancel := range []bool{false, true} {
		name := "before cancel"
		if endsDuringCancel {
			name = "during cancel"
		}
		t.Run(name, func(t *testing.T) {
			cancelCalls := 0
			s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost {
					cancelCalls++
					w := httptest.NewRecorder()
					w.WriteHeader(http.StatusServiceUnavailable)
					return w.Result(), nil
				}
				if endsDuringCancel && cancelCalls == 0 {
					return taskResponse(StateWorking, ""), nil
				}
				return taskResponse(StateCompleted, "Finished."), nil
			})
			got, err := s.Cancel(task.ID, "")
			wantCalls := 0
			if endsDuringCancel {
				wantCalls = 1
			}
			if err == nil || !strings.Contains(err.Error(), "already ended (completed)") || got.State != model.CompanionCompleted ||
				got.Result != "Finished." || cancelCalls != wantCalls || len(fleet.mail) != 1 {
				t.Fatalf("cancel: %+v, %v, calls=%d, mail=%q", got, err, cancelCalls, fleet.mail)
			}
		})
	}
}
func TestPollReconcilesEveryTrackedTask(t *testing.T) {
	var paths []string
	s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/a2a/v1/tasks/remote" {
			return nil, io.ErrUnexpectedEOF
		}
		return taskResponse(StateCompleted, "Second task finished."), nil
	})
	second := task
	second.ID, second.RemoteID = "second", "second-remote"
	second.State = model.CompanionInputRequired
	if err := s.o.Store.PutCompanionTask(second); err != nil {
		t.Fatal(err)
	}

	s.step()
	first, _ := s.Get(task.ID)
	got, _ := s.Get(second.ID)
	if first.State != model.CompanionUnknown || got.State != model.CompanionCompleted || len(paths) != 2 || len(fleet.mail) != 1 {
		t.Fatalf("states %s, %s, requests %v, mail %q", first.State, got.State, paths, fleet.mail)
	}
}

func TestFollowingBoundUnblocksQueuedSend(t *testing.T) {
	reads := 0
	s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		reads++
		if reads == 1 {
			w := httptest.NewRecorder()
			w.WriteHeader(http.StatusUnauthorized)
			return w.Result(), nil
		}
		return taskResponse(StateWorking, ""), nil
	})
	task.SentAt = model.Now() - (unreachableLimit - time.Minute).Milliseconds()
	if err := s.o.Store.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	queued := model.CompanionTask{ID: "queued", Kind: model.CompanionKindMessage, Origin: "owner",
		State: model.CompanionQueued, CreatedAt: model.Now()}
	if err := s.o.Store.PutCompanionTask(queued); err != nil {
		t.Fatal(err)
	}
	s.step()
	got, _ := s.Get(task.ID)
	waiting, _ := s.Get(queued.ID)
	if got.State != model.CompanionUnknown || !strings.Contains(waiting.Error, queueBlockedPrefix+task.ID) {
		t.Fatalf("block not shown: %+v; queued %+v", got, waiting)
	}
	entries := taskEntries(waiting, "", func(string) string { return "" })
	if entries[0].StatusNote != waiting.Error {
		t.Fatalf("owner cannot see queue blocker: %+v", entries)
	}
	// The next poll recovers the link, but the endpoint will say working forever.
	s.polls[task.ID] = pollSchedule{}
	s.step()
	got, _ = s.Get(task.ID)
	if got.State != model.CompanionWorking || reads != 2 {
		t.Fatalf("after reconnect: %+v, reads=%d", got, reads)
	}
	got.ProgressAt = model.Now() - unchangedRunLimit.Milliseconds()
	if err := s.o.Store.PutCompanionTask(got); err != nil {
		t.Fatal(err)
	}
	s.step()
	got, _ = s.Get(task.ID)
	waiting, _ = s.Get(queued.ID)
	if got.State != model.CompanionEndedWithoutReport || len(fleet.mail) != 1 ||
		!strings.Contains(fleet.mail[0], "no progress") || strings.HasPrefix(waiting.Error, queueBlockedPrefix) {
		t.Fatalf("bound: %+v, waiting %+v, mail %q", got, waiting, fleet.mail)
	}
}

func TestPollingBackoffIsIndependentAndCappedByFollowDeadline(t *testing.T) {
	count := map[string]int{}
	s, first, _ := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		count[r.URL.Path]++
		if strings.HasSuffix(r.URL.Path, "/remote") {
			w := httptest.NewRecorder()
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusServiceUnavailable)
			return w.Result(), nil
		}
		return taskResponse(StateWorking, ""), nil
	})
	first.SentAt = model.Now() - (unreachableLimit - time.Minute).Milliseconds()
	first.LastReadAt = first.SentAt
	if err := s.o.Store.PutCompanionTask(first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.RemoteID, second.SentAt = "second", "second-remote", model.Now()
	if err := s.o.Store.PutCompanionTask(second); err != nil {
		t.Fatal(err)
	}
	if got := s.step(); got > s.o.Poll {
		t.Fatalf("Retry-After slowed a healthy task: %s", got)
	}
	sched := s.polls[second.ID]
	sched.due = time.Time{}
	s.polls[second.ID] = sched
	s.step()
	if count["/a2a/v1/tasks/remote"] != 1 || count["/a2a/v1/tasks/second-remote"] != 2 {
		t.Fatalf("wrong tasks polled: %v", count)
	}
	sched = s.polls[first.ID]
	sched.due = time.Time{}
	s.polls[first.ID] = sched
	s.step()
	if s.polls[first.ID].failures != 2 || s.polls[second.ID].failures != 0 {
		t.Fatalf("wrong failures: %+v", s.polls)
	}
	first, _ = s.Get(first.ID)
	next, _ := s.followTasks([]model.CompanionTask{first}, 5*time.Minute)
	if next > time.Minute {
		t.Fatalf("Retry-After postponed the follow deadline: %s", next)
	}
}

func TestAnswerDuringReadOutage(t *testing.T) {
	posts := 0
	s, task, _ := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			posts++
			rt := Task{ID: "remote"}
			rt.Status.State = StateWorking
			_ = json.NewEncoder(w).Encode(map[string]any{"task": rt})
		}
		return w.Result(), nil
	})
	task.Kind, task.Origin = model.CompanionKindMessage, "owner"
	task.State, task.Question = model.CompanionInputRequired, "Which date?"
	askedLocked(&task, task.Question)
	if err := s.o.Store.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	s.poll(task)
	before, _ := s.Get(task.ID)
	if before.State != model.CompanionUnknown || !before.AwaitingAnswer() {
		t.Fatalf("question lost during outage: %+v", before)
	}
	entries := taskEntries(before, "", func(string) string { return "" })
	if entries[1].State != string(model.CompanionInputRequired) {
		t.Fatalf("conversation hid the open question: %+v", entries)
	}
	got, err := s.answerAbout(task.ID, "Friday", model.Owner, "")
	if err != nil || posts != 1 || got.State != model.CompanionWorking || got.LinkDownAt != 0 || got.Exchanges[0].Answer != "Friday" {
		t.Fatalf("answer: %+v, %v, posts=%d", got, err, posts)
	}
}

func TestLinkDownKeepsUnconfirmedCancelNote(t *testing.T) {
	up := false
	s, task, _ := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		if up {
			return taskResponse(StateWorking, ""), nil
		}
		w := httptest.NewRecorder()
		w.WriteHeader(http.StatusUnauthorized)
		return w.Result(), nil
	})
	task.Error = "Cancellation not confirmed by Companion; it may still be running."
	if err := s.o.Store.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	s.poll(task)
	unknown, _ := s.Get(task.ID)
	if !strings.Contains(unknown.Error, "Unknown: link down since ") || !strings.Contains(unknown.Error, task.Error) {
		t.Fatalf("both notes should be retained: %q", unknown.Error)
	}
	up = true
	s.poll(unknown)
	got, _ := s.Get(task.ID)
	if got.State != model.CompanionWorking || got.Error != task.Error {
		t.Fatalf("reconnect lost the cancel note: %+v", got)
	}
}

func TestCancelLiveRunWithLegacyStopMarker(t *testing.T) {
	var methods []string
	s, task, _ := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodGet {
			return taskResponse(StateWorking, ""), nil
		}
		return taskResponse(StateCanceled, ""), nil
	})
	got, err := s.Cancel(task.ID, "")
	if err != nil || got.State != model.CompanionCanceled || got.PollStoppedAt != 0 || strings.Join(methods, ",") != "GET,POST" {
		t.Fatalf("cancel: %+v, %v, methods %v", got, err, methods)
	}
}

func TestWorkingForThreeHoursWithProgressStaysFollowed(t *testing.T) {
	progress := 0
	s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		progress++
		w := httptest.NewRecorder()
		rt := Task{ID: "remote"}
		rt.Status.State = StateWorking
		rt.Status.Progress = json.RawMessage([]byte{byte('0' + progress)})
		_ = json.NewEncoder(w).Encode(rt)
		return w.Result(), nil
	})
	task.SentAt = model.Now() - (3 * time.Hour).Milliseconds()
	task.LastReadAt = model.Now() - time.Minute.Milliseconds()
	task.ProgressAt = task.SentAt
	if err := s.o.Store.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	s.step()
	got, _ := s.Get(task.ID)
	previous := got.ProgressKey
	s.polls[task.ID] = pollSchedule{}
	s.step()
	got, _ = s.Get(task.ID)
	if progress != 2 || got.State != model.CompanionWorking || got.ProgressAt <= task.ProgressAt ||
		got.ProgressKey == previous || got.LastReadAt < task.LastReadAt || len(fleet.mail) != 0 {
		t.Fatalf("task lost progress: %+v, progress=%d, mail=%q", got, progress, fleet.mail)
	}
}

func TestInputRequiredForThreeHoursStaysAnswerable(t *testing.T) {
	posts := 0
	s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected %s", r.Method)
		}
		posts++
		w := httptest.NewRecorder()
		rt := Task{ID: "remote"}
		rt.Status.State = StateWorking
		_ = json.NewEncoder(w).Encode(map[string]any{"task": rt})
		return w.Result(), nil
	})
	old := model.Now() - (3 * time.Hour).Milliseconds()
	task.SentAt, task.LastReadAt, task.ProgressAt = old, old, old
	task.Kind, task.Origin = model.CompanionKindMessage, "owner"
	task.State, task.Question = model.CompanionInputRequired, "Which date?"
	task.Exchanges = []model.CompanionExchange{{Question: task.Question, AskedAt: old}}
	if err := s.o.Store.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	queued := model.CompanionTask{ID: "queued", Origin: "owner", State: model.CompanionQueued,
		CreatedAt: model.Now(), Error: queueBlockedPrefix + task.ID + "."}
	if err := s.o.Store.PutCompanionTask(queued); err != nil {
		t.Fatal(err)
	}
	s.polls[task.ID] = pollSchedule{due: time.Now().Add(time.Minute)}
	s.step()
	got, _ := s.Get(task.ID)
	waiting, _ := s.Get(queued.ID)
	entries := taskEntries(got, "", func(string) string { return "" })
	if got.State != model.CompanionInputRequired || !got.QueueReleased || !got.AwaitingAnswer() ||
		!strings.Contains(entries[0].StatusNote, "stopped blocking the queue") || strings.HasPrefix(waiting.Error, queueBlockedPrefix) ||
		len(fleet.mail) != 0 {
		t.Fatalf("old question: %+v, queued %+v, entries %+v", got, waiting, entries)
	}
	got, err := s.answerAbout(task.ID, "Friday", model.Owner, "")
	if err != nil || posts != 1 || got.State != model.CompanionWorking || got.QueueReleased || got.Exchanges[0].Answer != "Friday" {
		t.Fatalf("answer: %+v %v, posts=%d", got, err, posts)
	}
}

func TestUnknownForTwoHoursEndsAndFreesQueue(t *testing.T) {
	reads := 0
	s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		reads++
		return nil, io.ErrUnexpectedEOF
	})
	task.State = model.CompanionUnknown
	task.SentAt = model.Now() - (6 * time.Hour).Milliseconds()
	task.LastReadAt = model.Now() - unreachableLimit.Milliseconds()
	task.LinkDownAt = task.LastReadAt + 1
	if err := s.o.Store.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	queued := model.CompanionTask{ID: "queued", Origin: "owner", State: model.CompanionQueued,
		CreatedAt: model.Now(), Error: queueBlockedPrefix + task.ID + "."}
	if err := s.o.Store.PutCompanionTask(queued); err != nil {
		t.Fatal(err)
	}
	s.step()
	got, _ := s.Get(task.ID)
	waiting, _ := s.Get(queued.ID)
	if reads != 1 || got.State != model.CompanionEndedWithoutReport || len(fleet.mail) != 1 ||
		!strings.Contains(fleet.mail[0], "for two hours") || strings.HasPrefix(waiting.Error, queueBlockedPrefix) {
		t.Fatalf("bound: %+v, queued %+v, mail %q", got, waiting, fleet.mail)
	}
}

func TestUnknownDeadlineFinalReadKeepsReport(t *testing.T) {
	reads := 0
	const report = "The final report arrived."
	s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		reads++
		return taskResponse(StateCompleted, report), nil
	})
	task.State, task.LastReadAt = model.CompanionUnknown, model.Now()-unreachableLimit.Milliseconds()
	task.LinkDownAt = task.LastReadAt + 1
	if err := s.o.Store.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	s.polls[task.ID] = pollSchedule{due: time.Now().Add(time.Hour)}
	s.step()
	got, _ := s.Get(task.ID)
	if reads != 1 || got.State != model.CompanionCompleted || got.Result != report ||
		len(fleet.mail) != 1 || !strings.Contains(fleet.mail[0], report) {
		t.Fatalf("final read: reads=%d task=%+v mail=%q", reads, got, fleet.mail)
	}
}

func TestLegacyUnknownSeedsFirstFailedReadAndKeepsKeyRefusal(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const refusal = "The old agent key has been revoked."
			s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]string{"detail": refusal})
				return w.Result(), nil
			})
			before := model.Now()
			old := before - (8 * time.Hour).Milliseconds()
			task.State, task.SentAt, task.CreatedAt, task.LinkDownAt = model.CompanionUnknown, old, old, old
			if err := s.o.Store.PutCompanionTask(task); err != nil {
				t.Fatal(err)
			}
			s.step()
			got, _ := s.Get(task.ID)
			if got.State != model.CompanionUnknown || got.LastReadAt < before || len(fleet.mail) != 0 ||
				!strings.Contains(got.Error, refusal) || !strings.Contains(got.Error, "refused tmx's key") {
				t.Fatalf("legacy first read: %+v, mail=%q", got, fleet.mail)
			}
			got.LastReadAt = model.Now() - unreachableLimit.Milliseconds()
			if err := s.o.Store.PutCompanionTask(got); err != nil {
				t.Fatal(err)
			}
			s.step()
			got, _ = s.Get(task.ID)
			if got.State != model.CompanionEndedWithoutReport || len(fleet.mail) != 1 || !strings.Contains(fleet.mail[0], refusal) {
				t.Fatalf("key refusal lost at expiry: %+v, mail=%q", got, fleet.mail)
			}
		})
	}
}

func TestTaskNotCancelableConflictKeepsFollowing(t *testing.T) {
	s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return taskResponse(StateWorking, ""), nil
		}
		w := httptest.NewRecorder()
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":{"message":"Cannot cancel","details":[{"reason":"TASK_NOT_CANCELABLE"}]}}`)
		return w.Result(), nil
	})
	_, err := s.Cancel(task.ID, "")
	got, _ := s.Get(task.ID)
	refused, ok := err.(*ReqError)
	if !ok || refused.Status != http.StatusConflict || !strings.Contains(refused.Msg, "cannot be cancelled") ||
		got.State != model.CompanionWorking || len(fleet.mail) != 0 {
		t.Fatalf("cancel refusal: %+v, %v, mail=%q", got, err, fleet.mail)
	}
}

func TestFailedMessageUnblocksNextMessageOnFirstRead(t *testing.T) {
	const dead, remote, next = "6ctjm4z277", "bf307085", "959mrh67nm"
	const failure = "The run ended in error."
	var requests []string
	s, task, fleet := taskEndpoint(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w := httptest.NewRecorder()
		switch r.URL.Path {
		case "/a2a/v1/tasks/" + remote:
			rt := Task{ID: remote}
			rt.Status.State, rt.Status.Message = StateFailed, &Message{Parts: []Part{{Text: failure}}}
			_ = json.NewEncoder(w).Encode(rt)
		case "/a2a/v1/message:send":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			rt := Task{ID: "next-run"}
			rt.Status.State = StateWorking
			_ = json.NewEncoder(w).Encode(map[string]any{"task": rt})
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		return w.Result(), nil
	})
	if err := config.WriteSecret(s.o.Paths.CompanionAgentTokenFile(), "fixture-agent-key"); err != nil {
		t.Fatal(err)
	}
	task.RemoteID, task.Kind, task.Body = remote, model.CompanionKindMessage, "First message"
	task.AgentID, task.SentAt = "head", model.Now()-time.Minute.Milliseconds()
	task.PollStoppedAt = 0
	if err := s.o.Store.PutCompanionTask(task); err != nil {
		t.Fatal(err)
	}
	// The fixture uses the store's stable local ID for the first item, while
	// the wire trace uses the original Companion run ID.
	queued := model.CompanionTask{ID: next, Kind: model.CompanionKindMessage, Origin: "agent", AgentID: "head",
		Body: "Proof message", Text: "Proof message", State: model.CompanionQueued, CreatedAt: model.Now()}
	if err := s.o.Store.PutCompanionTask(queued); err != nil {
		t.Fatal(err)
	}

	s.step()
	finished, _ := s.Get(task.ID)
	sent, _ := s.Get(next)
	if finished.State != model.CompanionFailed || finished.Error != failure || sent.State != model.CompanionWorking ||
		sent.RemoteID != "next-run" || len(fleet.mail) != 1 || fleet.agent != task.AgentID ||
		!strings.Contains(fleet.mail[0], failure) || strings.Join(requests, ",") != "GET /a2a/v1/tasks/"+remote+",POST /a2a/v1/message:send" {
		t.Fatalf("case %s: finished=%+v sent=%+v requests=%v mail=%q", dead, finished, sent, requests, fleet.mail)
	}
	if _, err := s.Cancel(task.ID, ""); err == nil || !strings.Contains(err.Error(), "message already ended (failed)") {
		t.Fatalf("cancel after reconciliation: %v", err)
	}
}
