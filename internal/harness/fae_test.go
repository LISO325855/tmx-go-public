package harness_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

func TestFaeRuntimeCapLogsOncePerModel(t *testing.T) {
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	dir := t.TempDir()
	prefix := strings.ToLower(filepath.Base(filepath.Dir(dir)))
	for _, suffix := range []string{"-first:latest", "-second:latest"} {
		name := prefix + suffix
		server := fakeOllama(t, name, 131072)
		t.Cleanup(server.Close)
		spec := runner.Spec{Harness: model.HarnessOpenCode, Command: "/fixture/opencode", Provider: model.FaeProvider,
			Model: "fae/" + name, OpenCodeDataDir: dir, OpenCodeBaseURL: server.URL + "/v1", FaeRuntimeContextCap: 131072}
		for _, cap := range []int{131072, 0, 0} {
			spec.FaeRuntimeContextCap = cap
			if _, err := harness.TerminalCommand(spec); err != nil {
				t.Fatal(err)
			}
		}
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one log per clipped model, got %q", output.String())
	}
	for i, suffix := range []string{"-first:latest", "-second:latest"} {
		var event struct {
			Model, Setting string
			Reported, Used int
		}
		if err := json.Unmarshal([]byte(lines[i]), &event); err != nil {
			t.Fatal(err)
		}
		if event.Model != "fae/"+prefix+suffix || event.Setting != "fae_runtime_context_cap" ||
			event.Reported != 131072 || event.Used != 32768 {
			t.Fatalf("incorrect cap event: %+v", event)
		}
	}
}

func TestFaeOpenCodeConfig(t *testing.T) {
	for _, tc := range []struct {
		name, selected      string
		reported, cap, want int
	}{
		{"default cap", "fae/gpt-oss:20b", 131072, 0, 32768},
		{"raised cap", "fae/gpt-oss:20b", 131072, 65536, 65536},
		{"reported limit", "fae/gpt-oss:20b", 131072, 262144, 131072},
		{"untagged model", "fae/qwen3-coder-next", 262144, 0, 32768},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := tc.selected
			tag, _ := model.FaeModelName(selected)
			server := fakeOllama(t, tag, tc.reported)
			defer server.Close()
			dataDir := t.TempDir()
			cmd, err := harness.TerminalCommand(runner.Spec{
				Harness: model.HarnessOpenCode, Command: "/fixture/opencode", Provider: model.FaeProvider,
				Model: selected, OpenCodeDataDir: dataDir, OpenCodeBaseURL: server.URL + "/v1", PermissionMode: "auto",
				FaeRuntimeContextCap: tc.cap,
			})
			if err != nil || !slices.Equal(cmd.Args, []string{"-m", selected}) ||
				!slices.Contains(cmd.Env, "XDG_DATA_HOME="+dataDir) {
				t.Fatalf("command: %+v, %v", cmd, err)
			}
			var cfg struct {
				Model       string   `json:"model"`
				SmallModel  string   `json:"small_model"`
				Enabled     []string `json:"enabled_providers"`
				Permissions any      `json:"permission"`
				Provider    map[string]struct {
					NPM     string            `json:"npm"`
					Options map[string]string `json:"options"`
					Models  map[string]struct {
						Limit    map[string]int `json:"limit"`
						ToolCall bool           `json:"tool_call"`
					} `json:"models"`
				} `json:"provider"`
			}
			for _, env := range cmd.Env {
				if raw, ok := strings.CutPrefix(env, "OPENCODE_CONFIG_CONTENT="); ok {
					if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
						t.Fatal(err)
					}
				}
			}
			provider := cfg.Provider["fae"]
			limit := provider.Models[tag].Limit
			if cfg.Model != selected || cfg.SmallModel != selected || !slices.Equal(cfg.Enabled, []string{"fae"}) ||
				cfg.Permissions != nil || provider.NPM != "@ai-sdk/openai-compatible" ||
				len(provider.Options) != 1 || provider.Options["baseURL"] != server.URL+"/v1" ||
				len(provider.Models) != 1 || !provider.Models[tag].ToolCall ||
				limit["context"] != tc.want ||
				limit["output"] <= 0 || limit["output"] >= limit["context"] {
				t.Fatalf("config: %+v", cfg)
			}
		})
	}
}

func TestFaeOpenCodeRefusesIneligibleModel(t *testing.T) {
	server := fakeOllama(t, "gpt-oss:20b", 0)
	defer server.Close()
	spec := runner.Spec{Harness: model.HarnessOpenCode, Command: "/fixture/opencode", Provider: model.FaeProvider,
		OpenCodeDataDir: t.TempDir(), OpenCodeBaseURL: server.URL + "/v1"}
	check := func(name, reason string) {
		t.Helper()
		spec.Model = name
		_, err := harness.TerminalCommand(spec)
		if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), reason) {
			t.Fatalf("launch %q: %v", name, err)
		}
	}
	check("fae/gpt-oss:120b", "absent from Fae")
	check("fae/gpt-oss:20b", "did not report a usable context length")
	server.Close()
	check("fae/gpt-oss:20b", "cannot read Fae's Ollama model list")
}

func fakeOllama(t *testing.T, name string, context int) *httptest.Server {
	t.Helper()
	tag := name
	if !strings.Contains(tag, ":") {
		tag += ":latest"
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/tags":
			fmt.Fprintf(w, `{"models":[{"name":%q}]}`, tag)
		case "POST /api/show":
			var req map[string]string
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req["model"] != name {
				t.Errorf("unexpected show request: %v, %v", req, err)
				http.Error(w, "wrong model", http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, `{"model_info":{"general.architecture":"fixture","unrelated.context_length":999,"fixture.context_length":%d}}`, context)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}
