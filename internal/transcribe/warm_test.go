package transcribe

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a log sink read while the service still writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func captureLog(e *testEnv) *syncBuffer {
	buf := &syncBuffer{}
	l := slog.New(slog.NewTextHandler(buf, nil))
	e.s.log, e.s.fw.log = l, l
	return buf
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// With faster-whisper as the engine the worker is started and its model loaded at the daemon's
// start, without audio; the first dictation then uses that same process.
func TestWorkerIsWarmedAtStartAndReusedByTheFirstDictation(t *testing.T) {
	e := fwEnv(t, "ok")
	log := captureLog(e)
	e.s.warmWorker() // what Start does (the test's service started before the engine was set)
	waitFor(t, "the worker is warm", func() bool { return strings.Contains(log.String(), "the faster-whisper worker is warm") })
	starts, jobs := e.fwRuns()
	if starts != 1 || len(jobs) != 1 || jobs[0]["warm"] != true || jobs[0]["audio"] != "" {
		t.Fatalf("warm-up: %d starts, requests %v", starts, jobs)
	}
	if !e.s.Status().Sidecar.Running {
		t.Fatal("the worker is not running after the warm-up")
	}
	res, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), "")
	if err != nil || !strings.HasPrefix(res.Text, "Fw words of ") {
		t.Fatalf("dictation after the warm-up: %+v %v", res, err)
	}
	if starts, jobs = e.fwRuns(); starts != 1 || len(jobs) != 2 {
		t.Fatalf("the dictation did not reuse the warm worker: %d starts, %d requests", starts, len(jobs))
	}

	// One line for the dictation: engine, audio length, time taken, language; never the text.
	waitFor(t, "the dictation's log line", func() bool { return strings.Contains(log.String(), "voice: transcribed") })
	var line string
	for _, l := range strings.Split(log.String(), "\n") {
		if strings.Contains(l, "voice: transcribed") {
			line = l
		}
	}
	for _, want := range []string{"engine=faster-whisper", "source=dictation", "audio_s=2", "took_s=", "language=fr", "chars="} {
		if !strings.Contains(line, want) {
			t.Fatalf("the log line lacks %s: %s", want, line)
		}
	}
	if strings.Contains(strings.ToLower(log.String()), "fw words") {
		t.Fatalf("the transcript's text is in the log: %s", log.String())
	}
}

// Nothing is warmed for whisper.cpp, and a worker that cannot load its model at start only
// leaves a warning: the daemon starts all the same.
func TestWarmUpIsOnlyForTheWorkerAndNeverFatal(t *testing.T) {
	plain := newEnv(t)
	plain.s.warmWorker()
	time.Sleep(100 * time.Millisecond)
	if st := plain.s.Status(); st.Sidecar != nil && st.Sidecar.Running {
		t.Fatal("a worker runs although the engine is whisper.cpp")
	}

	e := fwEnv(t, "fail")
	log := captureLog(e)
	e.s.warmWorker()
	waitFor(t, "the warning", func() bool { return strings.Contains(log.String(), "did not load its model at start") })
	// whisper.cpp still does the job.
	if res, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), ""); err != nil || res.Text == "" {
		t.Fatalf("dictation after a failed warm-up: %+v %v", res, err)
	}
}

func TestJobKindAndSeconds(t *testing.T) {
	for in, want := range map[string]string{"dictation": "dictation", "recording:r1/1": "recording", "upload:abc": "upload", "file": "file", "": "dictation"} {
		if got := jobKind(in); got != want {
			t.Errorf("jobKind(%q) = %q, want %q", in, got, want)
		}
	}
	if got := seconds(2449 * time.Millisecond); got != 2.4 {
		t.Errorf("seconds = %v", got)
	}
}
