package transcribe

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// remoteOpt sets config voice.remote_url/remote_model before Start, same pattern as peer_test.go's
// e.cfg.ViaPeer.
func remoteOpt(url, model string) envOpt {
	return func(e *testEnv, o *Options) { e.cfg.RemoteURL, e.cfg.RemoteModel = url, model }
}

// faeServer answers POST /v1/audio/transcriptions once, checking the OpenAI-style multipart
// fields the brief specifies, and returns the given verbose_json answer.
func faeServer(t *testing.T, wantModel string, answer remoteAnswer) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		if _, _, err := r.FormFile("file"); err != nil {
			t.Errorf("no file field: %v", err)
		}
		if got := r.FormValue("model"); got != wantModel {
			t.Errorf("model = %q, want %q", got, wantModel)
		}
		if got := r.FormValue("response_format"); got != "verbose_json" {
			t.Errorf("response_format = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(answer)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// closedPort returns an address nothing listens on, so a connection to it is refused at once.
func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr
}

func TestRemoteDictationUsesFaeAndNotTheLocalEngine(t *testing.T) {
	srv := faeServer(t, "large-v3-turbo", remoteAnswer{Text: "hello fae", Language: "en",
		Segments: []remoteSegment{{Start: 0, End: 1, Text: "hello fae"}}})
	e := newEnv(t, remoteOpt(srv.URL, "large-v3-turbo"))
	r, err := e.s.Dictate(context.Background(), fallbackWAV(false), "en")
	// Dictation always runs Tidy() (service.go dictateWithHostsOnce), same as whisper.cpp/faster-whisper
	// text: a capital first letter, here "hello" -> "Hello".
	if err != nil || r.Text != "Hello fae" || r.Language != "en" || r.Note != "" {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if runs := e.runs(); len(runs) != 0 {
		t.Fatalf("the local engine ran: %v", runs)
	}
}

func TestRemoteConnectFailureFallsBackToLocal(t *testing.T) {
	e := newEnv(t, remoteOpt(closedPort(t), "large-v3-turbo"))
	r, err := e.s.Dictate(context.Background(), fallbackWAV(false), "")
	if err != nil || r.Note != "Fae unreachable, used local" {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if runs := e.runs(); len(runs) != 1 {
		t.Fatalf("local engine runs = %v, want 1", runs)
	}
}

func TestRemote5xxFallsBackToLocal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	e := newEnv(t, remoteOpt(srv.URL, "large-v3-turbo"))
	r, err := e.s.Dictate(context.Background(), fallbackWAV(false), "")
	if err != nil || r.Note != "Fae unreachable, used local" {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if runs := e.runs(); len(runs) != 1 {
		t.Fatalf("local engine runs = %v, want 1", runs)
	}
}

func TestRemoteTimeoutFallsBackToLocal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		// Outlives the client's shortened deadline below, so the client gives up first; the
		// handler still returns on its own afterwards instead of blocking on the client closing
		// the connection (which it may not do synchronously), so httptest.Server.Close() in
		// t.Cleanup never hangs waiting for this connection to go idle.
		time.Sleep(4 * time.Second)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	oldMargin := timeoutMargin
	timeoutMargin = 50 * time.Millisecond
	t.Cleanup(func() { timeoutMargin = oldMargin })
	e := newEnv(t, remoteOpt(srv.URL, "large-v3-turbo"))
	r, err := e.s.Dictate(context.Background(), fallbackWAV(false), "")
	if err != nil || r.Note != "Fae unreachable, used local" {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if runs := e.runs(); len(runs) != 1 {
		t.Fatalf("local engine runs = %v, want 1", runs)
	}
}

// Fae can send 200 headers at once and then stall the body: Do() returns success immediately,
// and it is the later io.ReadAll that hits the deadline. That must fall back too.
func TestRemoteBodyTimeoutFallsBackToLocal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Outlives the client's shortened deadline below; the body never completes, but the
		// handler itself still returns afterwards so Close() in t.Cleanup does not hang.
		time.Sleep(4 * time.Second)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	oldMargin := timeoutMargin
	timeoutMargin = 50 * time.Millisecond
	t.Cleanup(func() { timeoutMargin = oldMargin })
	e := newEnv(t, remoteOpt(srv.URL, "large-v3-turbo"))
	r, err := e.s.Dictate(context.Background(), fallbackWAV(false), "")
	if err != nil || r.Note != "Fae unreachable, used local" {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if runs := e.runs(); len(runs) != 1 {
		t.Fatalf("local engine runs = %v, want 1", runs)
	}
}

// A canceled caller (not Fae) must not be mislabeled as a Fae timeout, and must not cause a
// wasted fallback run on the local engine for a job nobody is waiting for any more.
func TestRemoteParentCancellationIsNotMislabeledAsFaeTimeout(t *testing.T) {
	block := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		<-block // released in cleanup, so the server's Close() does not hang on it
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { close(block); srv.Close() })
	e := newEnv(t, remoteOpt(srv.URL, "large-v3-turbo"))
	log := captureLog(e)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, _ = e.s.Dictate(ctx, fallbackWAV(false), "") }()
	time.Sleep(100 * time.Millisecond) // let the request actually reach the (blocked) handler
	cancel()
	time.Sleep(300 * time.Millisecond) // give a buggy fallback a chance to have started
	if runs := e.runs(); len(runs) != 0 {
		t.Fatalf("the local engine ran after the caller went away: %v", runs)
	}
	if strings.Contains(log.String(), "Fae unreachable") {
		t.Fatalf("a canceled caller was logged as a Fae failure: %s", log.String())
	}
}

func TestRemoteBadAudioDoesNotFallBack(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "the file could not be decoded", http.StatusBadRequest)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	e := newEnv(t, remoteOpt(srv.URL, "large-v3-turbo"))
	_, err := e.s.Dictate(context.Background(), fallbackWAV(false), "")
	if err == nil {
		t.Fatal("want an error")
	}
	if runs := e.runs(); len(runs) != 0 {
		t.Fatalf("the local engine ran on a permanent rejection: %v", runs)
	}
}

func TestRemoteModelDefaultsWhenOmitted(t *testing.T) {
	srv := faeServer(t, DefaultRemoteModel, remoteAnswer{Text: "default model", Language: "en"})
	e := newEnv(t, remoteOpt(srv.URL, "")) // remote_url alone, as the brief shows
	if got := e.s.Status().Settings.RemoteModel; got != DefaultRemoteModel {
		t.Fatalf("remote model = %q, want %q", got, DefaultRemoteModel)
	}
	r, err := e.s.Dictate(context.Background(), fallbackWAV(false), "en")
	if err != nil || r.Text != "Default model" { // Tidy() capitalizes the first letter
		t.Fatalf("result=%+v err=%v", r, err)
	}
}

func TestRemoteURLSetSkipsLocalReadinessForEnqueue(t *testing.T) {
	srv := faeServer(t, "large-v3-turbo", remoteAnswer{Text: "a recording", Language: "en",
		Segments: []remoteSegment{{Start: 0, End: 1, Text: "a recording"}}})
	e := newEnv(t, noEngine, remoteOpt(srv.URL, "large-v3-turbo"))
	if err := e.s.Enqueue("recording:r1/1", e.wav("r1/seg-0001.wav", 1), ""); err != nil {
		t.Fatalf("Enqueue with no local engine set up: %v", err)
	}
	tr := e.wait("recording:r1/1")
	if tr.Text != "a recording" || tr.Model != "fae/large-v3-turbo" {
		t.Fatalf("%+v", tr)
	}
}

func TestRemoteURLSetDoesNotWarmTheLocalWorker(t *testing.T) {
	e := fwEnv(t, "ok")
	e.mu.Lock()
	e.cfg.RemoteURL, e.cfg.RemoteModel = "http://127.0.0.1:1", "large-v3-turbo"
	e.mu.Unlock()
	e.s.warmWorker()
	time.Sleep(100 * time.Millisecond)
	if starts, _ := e.fwRuns(); starts != 0 {
		t.Fatalf("the local worker was warmed although voice.remote_url is set: %d starts", starts)
	}
}
