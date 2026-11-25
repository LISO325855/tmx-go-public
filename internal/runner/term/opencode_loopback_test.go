package term

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestRealOpenCodeLoopback(t *testing.T) {
	executable := os.Getenv("TMX_TEST_OPENCODE_EXE")
	if executable == "" {
		t.Skip("set TMX_TEST_OPENCODE_EXE to an OpenCode executable to exercise the real TUI")
	}
	selected := model.FaeDefaultModel
	tag := strings.TrimPrefix(selected, "fae/")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/tags":
			fmt.Fprintf(w, `{"models":[{"name":%q}]}`, tag)
			return
		case "POST /api/show":
			fmt.Fprint(w, `{"model_info":{"general.architecture":"fixture","fixture.context_length":262144}}`)
			return
		}
		var body struct {
			Model    string
			Stream   bool
			Messages json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
			r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "" ||
			body.Model != tag || !body.Stream {
			t.Errorf("unexpected request %s, model %s, stream %v, decode %v", r.URL.Path, body.Model, body.Stream, err)
			http.Error(w, "unexpected request", 400)
			return
		}
		marker := "SAT9_LOOPBACK_TUI_OK"
		if strings.Contains(string(body.Messages), "Reply to the resumed turn.") {
			marker = "SAT9_LOOPBACK_RESUME_OK"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"sat9-fixture\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\""+tag+"\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\""+marker+"\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"sat9-fixture\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\""+tag+"\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	root := t.TempDir()
	cwd, dataDir, configDir := filepath.Join(root, "project"), filepath.Join(root, "data"), filepath.Join(root, "config")
	for _, dir := range []string{cwd, filepath.Join(configDir, "opencode")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	spec := runner.Spec{
		Harness: model.HarnessOpenCode, Command: executable, Cwd: cwd,
		Model: selected, Provider: model.FaeProvider, OpenCodeDataDir: dataDir, OpenCodeBaseURL: server.URL + "/v1", Cols: 120, Rows: 32,
	}
	cfg, err := harness.TerminalCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range cfg.Env {
		if data, ok := strings.CutPrefix(line, "OPENCODE_CONFIG_CONTENT="); ok {
			if err := os.WriteFile(filepath.Join(configDir, "opencode", "opencode.json"),
				[]byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	spec.Provider = "" // Read the generated config from the fixture file with its loopback URL.
	spec.Env = []string{
		"XDG_CONFIG_HOME=" + configDir, "XDG_DATA_HOME=" + dataDir,
		"XDG_CACHE_HOME=" + filepath.Join(root, "cache"), "XDG_STATE_HOME=" + filepath.Join(root, "state"),
		"OPENCODE_DISABLE_MODELS_FETCH=1",
	}
	p, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop(context.Background())
	defer func() {
		if t.Failed() {
			t.Logf("OpenCode screen: %q", p.(*process).w.scr.Lines())
		}
	}()
	ev := watchTUI(t, p)
	if err := p.Send("Reply with the mock marker."); err != nil {
		t.Fatal(err)
	}
	ev.until("mock reply", func(e runner.Event) bool { return strings.Contains(ev.out.String(), "SAT9_LOOPBACK_TUI_OK") })
	if ev.session == "" {
		ev.until("OpenCode session", func(e runner.Event) bool { return e.Kind == runner.EvSession })
	}
	if !strings.HasPrefix(ev.session, "ses_") {
		t.Fatal(ev.session)
	}
	ev.state(model.StateIdle)
	if err := p.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	spec.SessionID = ev.session
	resumed, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Stop(context.Background())
	resumeEvents := watchTUI(t, resumed)
	if err := resumed.Send("Reply to the resumed turn."); err != nil {
		t.Fatal(err)
	}
	resumeEvents.until("resumed OpenCode turn", func(e runner.Event) bool {
		return strings.Contains(resumeEvents.out.String(), "SAT9_LOOPBACK_RESUME_OK")
	})
	if resumeEvents.session != spec.SessionID {
		t.Fatalf("resumed %q, want %q", resumeEvents.session, spec.SessionID)
	}
}
