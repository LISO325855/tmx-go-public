package companion

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

// DefaultPort is Companion's local agent server port when nothing says otherwise
// (COMPANION_LOCAL_SIDECAR_PORT's default in Companion's main process).
const DefaultPort = 8010

// Address sources, for status displays.
const (
	SourceConfig      = "config"       // config.json companion.url
	SourceEngine      = "engine.json"  // Companion's agents/engine.json (written at engine start)
	SourceEngineState = "engine-state" // Companion's local-first-engine-state.json backend.port
	SourceDefault     = "default"      // 127.0.0.1:8010
)

// Located is where tmx looks for Companion.
type Located struct {
	URL    string `json:"url"`
	Source string `json:"source"`
}

// UserDataDir is Companion's Electron user-data folder (the first of UserDataDirs). Verified on macOS (v0.17.1); the
// Windows and Linux paths are Electron's defaults for the product name "Companion" (assumed).
func UserDataDir() string {
	if d := os.Getenv("TMXG_COMPANION_DATA"); d != "" { // tests
		return filepath.SplitList(d)[0]
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Companion")
	case "windows":
		if d := os.Getenv("APPDATA"); d != "" {
			return filepath.Join(d, "Companion")
		}
		return filepath.Join(home, "AppData", "Roaming", "Companion")
	default:
		if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
			return filepath.Join(d, "Companion")
		}
		return filepath.Join(home, ".config", "Companion")
	}
}

// UserDataDirs are the folders in which a Companion on this machine may advertise its engine:
// UserDataDir, and on Windows also %LOCALAPPDATA%\Companion, where the development build
// writes (17 Nov: its engine on port 8011 was not found, tmx looked in %APPDATA% only). The
// first folder with a live engine wins. TMXG_COMPANION_DATA may list several (tests).
func UserDataDirs() []string {
	if d := os.Getenv("TMXG_COMPANION_DATA"); d != "" {
		return filepath.SplitList(d)
	}
	dirs := []string{UserDataDir()}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			dirs = append(dirs, filepath.Join(d, "Companion"))
		}
	}
	return dirs
}

// Locate finds Companion's local agent server: the configured URL, else what Companion
// advertises in its user-data folder (only the address is read, never the secrets in those
// files), else the default port. It does not probe the address.
func Locate(configured string) (Located, error) {
	if configured != "" {
		u, err := CheckLoopback(configured)
		if err != nil {
			return Located{}, err
		}
		return Located{URL: u, Source: SourceConfig}, nil
	}
	dirs := UserDataDirs()
	for _, dir := range dirs {
		var engine struct {
			URL string `json:"url"`
			PID int    `json:"pid"`
		}
		if readJSON(filepath.Join(dir, "agents", "engine.json"), &engine) && engine.URL != "" && pidAlive(engine.PID) {
			if u, err := CheckLoopback(engine.URL); err == nil {
				return Located{URL: u, Source: SourceEngine}, nil
			}
		}
	}
	for _, dir := range dirs {
		var state struct {
			Backend struct {
				Port int `json:"port"`
				PID  int `json:"pid"`
			} `json:"backend"`
		}
		if readJSON(filepath.Join(dir, "local-first-engine-state.json"), &state) && state.Backend.Port > 0 && state.Backend.Port < 65536 &&
			pidAlive(state.Backend.PID) {
			return Located{URL: fmt.Sprintf("http://127.0.0.1:%d", state.Backend.Port), Source: SourceEngineState}, nil
		}
	}
	return Located{URL: fmt.Sprintf("http://127.0.0.1:%d", DefaultPort), Source: SourceDefault}, nil
}

// readJSON decodes only the fields v names (the files also hold secrets tmx never keeps).
func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 1<<20 {
		return false
	}
	return json.Unmarshal(b, v) == nil
}

func pidAlive(pid int) bool { return pid > 0 && sysinfo.Alive(pid) }
