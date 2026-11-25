package transcribe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A model folder is accepted only with exactly the pinned files: every one present with its
// SHA-256, nothing else beside them.
func TestVerifyModelFiles(t *testing.T) {
	const helloSHA = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" // sha256("hello")
	const worldSHA = "486ea46224d1bb4fb680f34f7c9ad96a8f24ec88be73ea8e5a6c65260e9cb8a7" // sha256("world")
	m := fwModel{Folder: "faster-whisper-test", Files: map[string]string{"model.bin": helloSHA, "config.json": worldSHA}}
	write := func(dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	good := func() string {
		dir := t.TempDir()
		write(dir, "model.bin", "hello")
		write(dir, "config.json", "world")
		write(dir, fwRevisionFile, "abc\n") // dot files are not model files
		if err := os.Mkdir(filepath.Join(dir, ".cache"), 0o700); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	if err := fwVerifyModel(good(), m); err != nil {
		t.Fatalf("the pinned files were refused: %v", err)
	}
	for name, breakIt := range map[string]func(dir string){
		"a changed weight file": func(dir string) { write(dir, "model.bin", "hellO") },
		"a changed small file":  func(dir string) { write(dir, "config.json", "world\n") },
		"a missing file":        func(dir string) { os.Remove(filepath.Join(dir, "config.json")) },
		"an extra file":         func(dir string) { write(dir, "extra.py", "print(1)") },
		"an extra folder":       func(dir string) { os.Mkdir(filepath.Join(dir, "plugins"), 0o700) },
		"a folder for a file": func(dir string) {
			os.Remove(filepath.Join(dir, "config.json"))
			os.Mkdir(filepath.Join(dir, "config.json"), 0o700)
		},
	} {
		dir := good()
		breakIt(dir)
		if err := fwVerifyModel(dir, m); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := fwVerifyModel(good(), fwModel{Folder: "unpinned"}); err == nil || !strings.Contains(err.Error(), "no file hashes") {
		t.Errorf("a model without pinned hashes: %v", err)
	}
	// The real models carry hashes for every file the downloader fetches, 64 hex digits each.
	for k, real := range fwModels {
		if _, ok := real.Files["model.bin"]; !ok || len(real.Files) < 4 {
			t.Errorf("%s: pinned files %v", k, real.Files)
		}
		for name, sum := range real.Files {
			if len(sum) != 64 || strings.ToLower(sum) != sum {
				t.Errorf("%s/%s: %q is not a lowercase SHA-256", k, name, sum)
			}
		}
	}
}

// The worker's Python is started isolated and with nothing of the daemon's environment that
// could redirect it.
func TestWorkerStartIsIsolated(t *testing.T) {
	if got := strings.Join(fwWorkerArgs("w.py"), " "); got != "-I -X utf8 -u w.py" {
		t.Fatalf("worker arguments: %q", got)
	}
	t.Setenv("PYTHONHOME", "C:/elsewhere")
	t.Setenv("PYTHONPATH", "C:/elsewhere/lib")
	t.Setenv("PYTHONSTARTUP", "C:/elsewhere/start.py")
	t.Setenv("TMX_VOICE_MODEL", "from-the-daemon")
	env := fwSpec{Model: "the-model", ToolsDir: ""}.env()
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		up := strings.ToUpper(k)
		if (strings.HasPrefix(up, "PYTHON") && up != "PYTHONIOENCODING") || v == "from-the-daemon" {
			t.Errorf("the worker's environment carries %s", kv)
		}
	}
	if !containsEnv(env, "TMX_VOICE_MODEL=the-model") {
		t.Errorf("the worker's own settings are missing: %v", env)
	}
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}
