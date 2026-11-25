package transcribe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

// The faster-whisper worker's setup (`tmx voice setup` with voice.engine = "faster-whisper"),
// the steps of Python tmx's `tmx voice install`, into this daemon's own state:
//
//  1. python -m venv <state>/voice/fw/venv            (with a Python found on this machine)
//  2. pip install --require-hashes --no-deps --only-binary :all: -r locks/<platform>.txt
//     (faster-whisper 1.2.1, av<19 and every dependency, each pinned with its hashes)
//  3. does CTranslate2 see a GPU?
//  4. Windows with a GPU: cuBLAS and the CUDA runtime, hash-pinned too (locks/cuda-win-x64.txt)
//  5. the model(s) into <state>/voice/fw/models at a pinned commit: large-v3-turbo and small
//     with a GPU, small only without
//     each file then checked against its pinned SHA-256 (fwVerifyModel)
//
// With voice.python set, the owner's Python has faster-whisper already: only a missing model
// is fetched (through that Python). Downloads come from PyPI (hash-locked) and the Hugging
// Face repositories below; nothing of another program's state is read or written.

// fwModel is a CTranslate2 model: its Hugging Face repository, folder name, pinned commit and
// the SHA-256 of every file of that commit the worker reads. A download is accepted only when
// each of them is there with exactly that content (fwVerifyModel): the commit pins what is
// asked for, the hashes what arrived.
type fwModel struct {
	Repo, Folder, Revision string
	Files                  map[string]string // file name -> lowercase hex SHA-256
}

// The hashes are those of the pinned commits (17 Nov 2026): model.bin is the repository's own
// LFS object id; the small files were fetched at that commit, checked against its git blob ids
// and hashed.
var fwModels = map[string]fwModel{
	"turbo": {"dropbox-dash/faster-whisper-large-v3-turbo", fwTurbo, "0a363e9161cbc7ed1431c9597a8ceaf0c4f78fcf", map[string]string{
		"config.json":              "b0253ea6c0d3bea6b1e19e91a02acfd3b53f4467362efcb5a3e6b16c9b3a9b7e",
		"model.bin":                "e76620f83d5f5b69efd3d87e3dc180c1bd21df9fbebacfd4335e5e1efcc018da",
		"preprocessor_config.json": "7ccc62c6f2765af1f3b46c00c9b5894426835a05021c8b9c01eecb6dfb542711",
		"tokenizer.json":           "297b13372ac43916285644fb9687add3cc62ee2a1adb60da3dc25cc94c1871fd",
		"vocabulary.json":          "c69260f2ab26d659b7c398f9a2b2b48ed0df16c3b47d7326782fd9cba71690c1",
	}},
	"small": {"Systran/faster-whisper-small", fwSmall, "536b0662742c02347bc0e980a01041f333bce120", map[string]string{
		"config.json": "b55496ac7940a7ae47d2c01eab40edfd8701feec1229d9cce3b40014383fb828",
		"model.bin":   "3e305921506d8872816023e4c273e75d2419fb89b24da97b4fe7bce14170d671",
		"tokenizer.json": "fb7b63191e9bb045" +
			"082c79fd742a3106" +
			"a12c99513ab30df4" +
			"a0d47fa6cb6fd0ab",
		"vocabulary.txt": "34ce3fe1c5041027b3f8d42912270993f986dbc4bb34cf27f951e34a1e453913",
	}},
}

// fwVerifyModel checks a model folder against m's pinned files: each one present with its
// SHA-256, and no other regular file beside them (dot files, such as the revision marker and
// the downloader's cache folder, aside).
func fwVerifyModel(dir string, m fwModel) error {
	if len(m.Files) == 0 {
		return fmt.Errorf("no file hashes are pinned for %s", m.Folder)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, ok := m.Files[e.Name()]; !ok || !e.Type().IsRegular() {
			return fmt.Errorf("%s holds something that is not part of the pinned model: %s", m.Folder, e.Name())
		}
	}
	for name, want := range m.Files {
		got, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if got != want {
			return fmt.Errorf("%s of %s is not the pinned file (SHA-256 %s, expected %s)", name, m.Folder, got, want)
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fwRevisionFile holds the commit a model folder was installed at.
const fwRevisionFile = ".tmx-revision"

// fwLockKey names the hash-locked requirements for a platform ("" = there are none: refused).
func fwLockKey(goos, goarch string) string {
	switch goos + "/" + goarch {
	case "windows/amd64":
		return "win-x64"
	case "darwin/arm64":
		return "mac-arm64"
	case "linux/amd64":
		return "linux-x64"
	}
	return ""
}

// fwExec runs one setup step and returns its output (a variable: tests use a fake).
var fwExec = func(ctx context.Context, env []string, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	cmd := hiddenexec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(clip(out, 400)))
		return string(out), fmt.Errorf("%v: %s", err, text)
	}
	return string(out), nil
}

// fwLookPath finds a program on PATH (a variable for tests).
var fwLookPath = exec.LookPath

// fwSetupEnv: the daemon's environment without what would redirect Python or keep the model
// download offline.
func fwSetupEnv() []string {
	env := []string{"PIP_NO_INPUT=1", "PYTHONIOENCODING=utf-8", "HF_HUB_DISABLE_SYMLINKS_WARNING=1"}
	for _, kv := range os.Environ() {
		switch k, _, _ := strings.Cut(kv, "="); strings.ToUpper(k) {
		case "PYTHONPATH", "PYTHONHOME", "HF_HUB_OFFLINE", "PIP_NO_INPUT", "PYTHONIOENCODING":
		default:
			env = append(env, kv)
		}
	}
	return env
}

// fwBasePython finds a Python to make the venv with.
func (s *Service) fwBasePython(ctx context.Context, env []string) (string, error) {
	names := []string{"python3", "python"}
	if s.o.GOOS == "windows" {
		names = []string{"python", "python3", "py"}
	}
	for _, n := range names {
		p, err := fwLookPath(n)
		if err != nil {
			continue
		}
		// The Microsoft Store's python.exe stub prints nothing and fails; the real one names itself.
		out, err := fwExec(ctx, env, p, "-c", "import sys; print(sys.executable)")
		if exe := strings.TrimSpace(lastLine(out)); err == nil && filepath.IsAbs(exe) {
			return exe, nil
		}
	}
	return "", errors.New("no Python found on this machine: install Python 3.12, or set voice.python to one that has faster-whisper")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (s *Service) fwSetStep(step string) {
	s.mu.Lock()
	s.fwStep = step
	s.mu.Unlock()
	s.publish(true)
}

// setupSidecar installs what the worker is missing. Its error is also kept for Status.
func (s *Service) setupSidecar(ctx context.Context) error {
	err := s.setupSidecarSteps(ctx)
	s.mu.Lock()
	s.fwStep = ""
	if err != nil && ctx.Err() == nil {
		s.dlErrors["sidecar"] = err.Error()
	}
	s.mu.Unlock()
	s.publish(true)
	if err != nil {
		s.log.Warn("voice: the faster-whisper setup failed", "err", err)
	}
	return err
}

func (s *Service) setupSidecarSteps(ctx context.Context) error {
	c := s.o.Settings()
	env := fwSetupEnv()
	py := strings.TrimSpace(c.Python)
	own := py == ""
	if err := os.MkdirAll(s.fwDir(), 0o700); err != nil {
		return err
	}
	if own {
		key := fwLockKey(s.o.GOOS, s.o.GOARCH)
		if key == "" {
			return fmt.Errorf("the faster-whisper worker has no hash-locked package list for this platform (%s)", s.platform())
		}
		py = s.fwVenvPython()
		if !isFile(py) {
			s.fwSetStep("making the worker's Python environment")
			base, err := s.fwBasePython(ctx, env)
			if err != nil {
				return err
			}
			if _, err := fwExec(ctx, env, base, "-m", "venv", s.fwVenv()); err != nil {
				return fmt.Errorf("python -m venv failed: %w", err)
			}
		}
		s.fwSetStep("installing faster-whisper")
		if err := s.fwPip(ctx, env, py, key+".txt"); err != nil {
			return err
		}
	}
	cuda := false
	s.fwSetStep("looking for a GPU")
	if out, err := fwExec(ctx, env, py, "-c", "import ctranslate2; print(ctranslate2.get_cuda_device_count())"); err == nil {
		n, _ := strconv.Atoi(lastLine(out))
		cuda = n > 0
	} else if !own {
		return fmt.Errorf("voice.python has no CTranslate2 (is faster-whisper installed there?): %w", err)
	}
	if own && cuda && s.o.GOOS == "windows" && s.o.GOARCH == "amd64" {
		s.fwSetStep("installing the GPU libraries")
		if err := s.fwPip(ctx, env, py, "cuda-win-x64.txt"); err != nil {
			return err
		}
	}
	if strings.TrimSpace(c.ModelDir) != "" {
		return nil // the owner's model folder: nothing to download
	}
	want := []string{"small"}
	if cuda {
		want = []string{"turbo", "small"}
	}
	for _, k := range want {
		m := fwModels[k]
		dir := filepath.Join(s.fwModelsDir(), m.Folder)
		if rev, _ := os.ReadFile(filepath.Join(dir, fwRevisionFile)); fwModelReady(dir) && strings.TrimSpace(string(rev)) == m.Revision {
			if fwVerifyModel(dir, m) == nil {
				continue
			}
			// Not the pinned files (changed on disk, or installed before the hashes were
			// pinned): fetched again below, and checked.
			_ = os.Remove(filepath.Join(dir, fwRevisionFile))
		}
		s.fwSetStep("downloading the " + strings.TrimPrefix(m.Folder, "faster-whisper-") + " model")
		code := fmt.Sprintf("from faster_whisper.utils import download_model as d; d(%s, output_dir=%s, revision=%s)", pyStr(m.Repo), pyStr(dir), pyStr(m.Revision))
		if _, err := fwExec(ctx, env, py, "-c", code); err != nil {
			return fmt.Errorf("the %s model's download failed: %w", k, err)
		}
		if !fwModelReady(dir) {
			return fmt.Errorf("the %s model's download left no model.bin in %s", k, dir)
		}
		s.fwSetStep("checking the " + strings.TrimPrefix(m.Folder, "faster-whisper-") + " model")
		if err := fwVerifyModel(dir, m); err != nil {
			return fmt.Errorf("the downloaded %s model was refused: %w", k, err)
		}
		if err := os.WriteFile(filepath.Join(dir, fwRevisionFile), []byte(m.Revision+"\n"), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// fwPip installs one embedded lock file into the venv: only hashed, only wheels.
func (s *Service) fwPip(ctx context.Context, env []string, py, lock string) error {
	b, err := lockFS.ReadFile("sidecar/locks/" + lock)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.fwDir(), "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file := filepath.Join(dir, lock)
	if err := os.WriteFile(file, b, 0o600); err != nil {
		return err
	}
	if _, err := fwExec(ctx, env, py, "-m", "pip", "install", "--disable-pip-version-check", "--require-hashes", "--no-deps",
		"--only-binary", ":all:", "-r", file); err != nil {
		return fmt.Errorf("pip failed (%s): %w", lock, err)
	}
	return nil
}

// pyStr is s as a Python string literal.
func pyStr(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}
