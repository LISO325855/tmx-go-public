package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

var faeRuntimeCapLogged sync.Map // canonical Ollama tag -> logged in this process

func faeContext(baseURL, name string, runtimeCap int) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	root := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
	qualified := model.FaeProvider + "/" + name
	tag := ollamaTag(name)
	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := ollamaMetadata(ctx, http.MethodGet, root+"/api/tags", nil, &tags); err != nil {
		return 0, fmt.Errorf("cannot use model %q: cannot read Fae's Ollama model list: %w", qualified, err)
	}
	for _, entry := range tags.Models {
		if entry.Name == tag || entry.Model == tag || entry.Name == name || entry.Model == name {
			var show struct {
				Info map[string]json.RawMessage `json:"model_info"`
			}
			body, _ := json.Marshal(map[string]string{"model": name})
			if err := ollamaMetadata(ctx, http.MethodPost, root+"/api/show", body, &show); err != nil {
				return 0, fmt.Errorf("cannot use model %q: Fae's Ollama model details failed: %w", qualified, err)
			}
			var arch string
			var size int
			if json.Unmarshal(show.Info["general.architecture"], &arch) != nil || arch == "" ||
				json.Unmarshal(show.Info[arch+".context_length"], &size) != nil || size <= 1 {
				return 0, fmt.Errorf("cannot use model %q: Fae's Ollama did not report a usable context length", qualified)
			}
			// Ollama's /v1 API cannot raise the server's runtime context.
			if runtimeCap <= 1 {
				runtimeCap = model.FaeDefaultRuntimeContextCap
			}
			used := min(size, runtimeCap)
			if override := model.FaeContextOverride(tag); override > 1 {
				used = min(used, override)
			}
			if runtimeCap < size {
				if _, logged := faeRuntimeCapLogged.LoadOrStore(tag, struct{}{}); !logged {
					slog.Info("Fae runtime cap limits model context", "model", model.FaeProvider+"/"+tag,
						"reported", size, "used", used, "setting", "fae_runtime_context_cap")
				}
			}
			return used, nil
		}
	}
	return 0, fmt.Errorf("cannot use model %q: it is absent from Fae's Ollama model list", qualified)
}

func ollamaTag(name string) string {
	if last := name[strings.LastIndexByte(name, '/')+1:]; !strings.Contains(last, ":") {
		return name + ":latest"
	}
	return name
}

func ollamaMetadata(ctx context.Context, method, url string, body []byte, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", req.URL.Path, res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(result)
}
