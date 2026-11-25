package transcribe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// fakeFW is the test binary as the faster-whisper worker: JSON lines in, JSON lines out, like
// sidecar/voice_worker.py. Modes: ok, held, progress (two progress lines before each answer),
// fail (ok:false), garbage, slow (20 s per job), die (leaves at once, like a Python without
// faster-whisper). TMXG_FAKE_FW_LOG gets "start <pid>" and a JSON line per job.
func fakeFW(mode string) int {
	logf := func(format string, a ...any) {
		if p := os.Getenv("TMXG_FAKE_FW_LOG"); p != "" {
			f, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			fmt.Fprintf(f, format+"\n", a...)
			f.Close()
		}
	}
	logf("start %d", os.Getpid())
	if mode == "die" {
		fmt.Fprintln(os.Stderr, "ModuleNotFoundError: No module named 'faster_whisper'")
		return 1
	}
	out := bufio.NewWriter(os.Stdout)
	say := func(v any) {
		b, _ := json.Marshal(v)
		out.Write(append(b, '\n'))
		out.Flush()
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var req map[string]any
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			say(map[string]any{"ok": false, "error": "bad request"})
			continue
		}
		req["env_model"], req["env_tools"], req["env_device"] = os.Getenv("TMX_VOICE_MODEL"), os.Getenv("TMX_VOICE_TOOLS"), os.Getenv("TMX_VOICE_DEVICE")
		b, _ := json.Marshal(req)
		logf("job %s", b)
		if warm, _ := req["warm"].(bool); warm { // the daemon's start: load the model, no audio
			if mode == "slowwarm" {
				time.Sleep(20 * time.Second)
			}
			if mode == "fail" {
				say(map[string]any{"ok": false, "error": "RuntimeError: CUDA failed with error out of memory"})
			} else {
				say(map[string]any{"ok": true, "text": "", "warm": true, "device": "cuda", "model": filepath.Base(os.Getenv("TMX_VOICE_MODEL"))})
			}
			continue
		}
		audio, _ := req["audio"].(string)
		dur := 3.0
		if w, err := readWAV(audio); err == nil {
			dur = w.Duration().Seconds()
		}
		switch mode {
		case "fail":
			say(map[string]any{"ok": false, "error": "RuntimeError: CUDA failed with error out of memory"})
			continue
		case "garbage":
			fmt.Fprintln(out, "Traceback (most recent call last):")
			out.Flush()
			continue
		case "slow":
			time.Sleep(20 * time.Second)
		case "progress":
			// Written by hand: "progress" comes first, as Python's json.dumps writes it.
			fmt.Fprintf(out, `{"progress": %.1f, "duration": %.1f}`+"\n", dur/2, dur)
			out.Flush()
			time.Sleep(300 * time.Millisecond)
			fmt.Fprintf(out, `{"progress": %.1f, "duration": %.1f}`+"\n", dur, dur)
			out.Flush()
			time.Sleep(300 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		lang, _ := req["language"].(string)
		if lang == "" {
			lang = "fr"
			if mode == "outside" {
				lang = "is"
			}
		}
		text := "fw words of " + filepath.Base(audio)
		res := map[string]any{"ok": true, "text": text, "language": lang, "duration": dur, "device": "cuda",
			"model":    filepath.Base(os.Getenv("TMX_VOICE_MODEL")),
			"segments": []map[string]any{{"start": 0.0, "end": dur / 2, "text": "fw words"}, {"start": dur / 2, "end": dur, "text": " of " + filepath.Base(audio)}}}
		if mode == "held" {
			res["held"], res["held_reason"] = true, "suspect"
		}
		say(res)
	}
	return 0
}

// fwEnv is newEnv with voice.engine = faster-whisper: the fake worker as voice.python and a
// (fake) model folder. whisper.cpp stays installed as the fallback unless opts say otherwise.
func fwEnv(t *testing.T, mode string, opts ...envOpt) *testEnv {
	t.Helper()
	t.Setenv("TMXG_FAKE_FW", mode)
	e := newEnv(t, opts...)
	t.Setenv("TMXG_FAKE_FW_LOG", e.fwLog())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.home, "fwmodels", fwTurbo)
	os.MkdirAll(dir, 0o700)
	if err := os.WriteFile(filepath.Join(dir, "model.bin"), []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.cfg.Engine, e.cfg.Python, e.cfg.ModelDir = config.EngineFasterWhisper, exe, dir
	e.cfg.Prompt, e.cfg.Hotwords = "Vocabulaire / vocabulary: Tiamatrix, tmx.", "Tiamatrix"
	e.mu.Unlock()
	return e
}

func (e *testEnv) fwLog() string { return filepath.Join(e.home, "fw.log") }

// fwRuns reads the fake worker's log: how many processes started, and the jobs' requests.
func (e *testEnv) fwRuns() (starts int, jobs []map[string]any) {
	b, _ := os.ReadFile(e.fwLog())
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "start ") {
			starts++
		} else if j, ok := strings.CutPrefix(l, "job "); ok {
			var m map[string]any
			if json.Unmarshal([]byte(j), &m) == nil {
				jobs = append(jobs, m)
			}
		}
	}
	return starts, jobs
}

func TestSidecarOneWorkerDoesTheJobs(t *testing.T) {
	e := fwEnv(t, "ok")
	st := e.s.Status()
	if !st.Ready || st.Settings.Engine != config.EngineFasterWhisper || st.Sidecar == nil || !st.Sidecar.Ready || st.Sidecar.Running || !st.Sidecar.Fallback {
		t.Fatalf("status: %+v sidecar %+v", st, st.Sidecar)
	}
	for i, lang := range []string{"", "de"} {
		src := fmt.Sprintf("recording:r%d/1", i)
		p := e.wav(fmt.Sprintf("r%d/seg-0001.wav", i), 4)
		if err := e.s.Enqueue(src, p, lang); err != nil {
			t.Fatal(err)
		}
		tr := e.wait(src)
		if tr.State != model.TranscriptDone || tr.Text != "fw words of seg-0001.wav" || tr.Model != "faster-whisper/large-v3-turbo" {
			t.Fatalf("%+v", tr)
		}
		if want := map[bool]string{true: "fr", false: "de"}[lang == ""]; tr.Language != want {
			t.Fatalf("language %q, want %q", tr.Language, want)
		}
		if len(tr.Segments) != 2 || tr.Segments[0].ToMS != 2000 || tr.Segments[1].FromMS != 2000 || tr.Segments[1].ToMS != 4000 || tr.Segments[1].Text != "of seg-0001.wav" {
			t.Fatalf("segments %+v", tr.Segments)
		}
	}
	starts, jobs := e.fwRuns()
	if starts != 1 || len(jobs) != 2 {
		t.Fatalf("the model must stay loaded in ONE process: %d starts, %d jobs", starts, len(jobs))
	}
	j := jobs[0]
	if j["initial_prompt"] != "Vocabulaire / vocabulary: Tiamatrix, tmx." || j["hotwords"] != "Tiamatrix" || j["language"] != nil {
		t.Fatalf("request: %v", j)
	}
	if jobs[1]["language"] != "de" || j["env_device"] != "auto" || j["env_tools"] != "" || !strings.HasSuffix(j["env_model"].(string), fwTurbo) {
		t.Fatalf("request/env: %v %v", jobs[1], j)
	}
	if runs := e.runs(); len(runs) != 0 {
		t.Fatalf("whisper.cpp ran although the worker answered: %v", runs)
	}
	if !e.s.Status().Sidecar.Running {
		t.Fatal("the worker left after its job: the model would load again for every job")
	}
	// The embedded script is what Python is given.
	if b, err := os.ReadFile(e.s.fwScript()); err != nil || !bytes.Equal(b, workerScript) {
		t.Fatalf("the worker script on disk is not the embedded one (%v)", err)
	}
}

// Dictation, uploaded notes and recorder segments all use the same resident model.
func TestSidecarSharedAcrossVoiceSources(t *testing.T) {
	e := fwEnv(t, "ok")
	if _, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.TranscribeFile(context.Background(), wavReader(t, e, 3), ""); err != nil {
		t.Fatal(err)
	}
	const source = "recording:shared/1"
	if err := e.s.Enqueue(source, e.wav("shared.wav", 4), ""); err != nil {
		t.Fatal(err)
	}
	if tr := e.wait(source); tr.State != model.TranscriptDone {
		t.Fatalf("recording: %+v", tr)
	}
	if starts, jobs := e.fwRuns(); starts != 1 || len(jobs) != 3 {
		t.Fatalf("voice sources must share ONE model process: %d starts, %d jobs", starts, len(jobs))
	}
	if len(e.runs()) != 0 {
		t.Fatal("a second engine was used")
	}
}

func TestSidecarFallsBackToWhisperCpp(t *testing.T) {
	for _, mode := range []string{"die", "fail", "garbage"} {
		t.Run(mode, func(t *testing.T) {
			e := fwEnv(t, mode)
			p := e.wav("r1/seg-0001.wav", 3)
			if err := e.s.Enqueue("recording:r1/1", p, ""); err != nil {
				t.Fatal(err)
			}
			tr := e.wait("recording:r1/1")
			if tr.State != model.TranscriptDone || tr.Text != "fake words of seg-0001.wav" || tr.Model != DefaultModel {
				t.Fatalf("%+v", tr)
			}
			if starts, _ := e.fwRuns(); starts != 1 {
				t.Fatalf("the worker was not tried first (%d starts)", starts)
			}
		})
	}
}

func TestSidecarTooSlowIsStoppedAndFallsBack(t *testing.T) {
	oldBase, oldMax, oldLoad := fwGPUBase, fwGPUMax, fwLoadWait
	fwGPUBase, fwGPUMax, fwLoadWait = 0, 400*time.Millisecond, 0
	t.Cleanup(func() { fwGPUBase, fwGPUMax, fwLoadWait = oldBase, oldMax, oldLoad })
	e := fwEnv(t, "slow")
	p := e.wav("r1/seg-0001.wav", 3)
	start := time.Now()
	if err := e.s.Enqueue("recording:r1/1", p, ""); err != nil {
		t.Fatal(err)
	}
	tr := e.wait("recording:r1/1")
	if tr.State != model.TranscriptDone || tr.Model != DefaultModel {
		t.Fatalf("%+v", tr)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("the time limit did not cut the worker off")
	}
	if e.s.fw.running() {
		t.Fatal("a worker that answered too late still runs: its late answer would reach the next job")
	}
}

func TestSidecarProgressLinesRestartTheTimeLimit(t *testing.T) {
	e := fwEnv(t, "progress")
	if _, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), ""); err != nil { // the worker is up
		t.Fatal(err)
	}
	oldBase, oldMax := fwGPUBase, fwGPUMax
	fwGPUBase, fwGPUMax = 0, 500*time.Millisecond // each line comes within it; all of them together do not
	t.Cleanup(func() { fwGPUBase, fwGPUMax = oldBase, oldMax })
	res, err := e.s.TranscribeFile(context.Background(), wavReader(t, e, 3), "")
	if err != nil || !strings.HasPrefix(res.Text, "fw words of file-") {
		t.Fatalf("%+v %v", res, err)
	}
	if len(e.runs()) != 0 {
		t.Fatal("whisper.cpp ran: the progress lines did not restart the time limit")
	}
}

func wavReader(t *testing.T, e *testEnv, secs float64) *bytes.Reader {
	t.Helper()
	p := filepath.Join(e.home, "in.wav")
	writeWAV(t, p, secs)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

func TestSidecarWithoutFallbackFailsWithTheReason(t *testing.T) {
	e := fwEnv(t, "die", noEngine)
	if st := e.s.Status(); !st.Ready || st.Sidecar.Fallback {
		t.Fatalf("status: %+v %+v", st, st.Sidecar)
	}
	p := e.wav("r1/seg-0001.wav", 3)
	if err := e.s.Enqueue("recording:r1/1", p, ""); err != nil {
		t.Fatal(err)
	}
	tr := e.wait("recording:r1/1")
	if tr.State != model.TranscriptFailed || !strings.Contains(tr.Error, "faster-whisper worker stopped") || !strings.Contains(tr.Error, "No module named 'faster_whisper'") {
		t.Fatalf("%+v", tr)
	}
}

func TestSidecarNotSetUp(t *testing.T) {
	// Without whisper.cpp: not ready, and the reason names the worker's setup.
	e := newEnv(t, noEngine)
	e.mu.Lock()
	e.cfg.Engine = config.EngineFasterWhisper
	e.mu.Unlock()
	st := e.s.Status()
	if st.Ready || !strings.Contains(st.Reason, "faster-whisper worker is not installed") || st.Sidecar == nil || st.Sidecar.Ready {
		t.Fatalf("status: %+v", st)
	}
	if _, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), ""); !errors.Is(err, ErrNotSetUp) {
		t.Fatalf("dictation: %v", err)
	}
	// With whisper.cpp set up, it does the jobs until the worker is installed.
	e = newEnv(t)
	e.mu.Lock()
	e.cfg.Engine = config.EngineFasterWhisper
	e.cfg.Python = filepath.Join(e.home, "no-such-python")
	e.mu.Unlock()
	if st := e.s.Status(); !st.Ready || st.Sidecar.Ready || !strings.Contains(st.Sidecar.Reason, "voice.python") {
		t.Fatalf("status: %+v %+v", st, st.Sidecar)
	}
	res, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), "")
	if err != nil || !strings.HasPrefix(res.Text, "Fake words of dictation-") {
		t.Fatalf("%+v %v", res, err)
	}
}

func wavBytes(t *testing.T, e *testEnv, secs float64) []byte {
	t.Helper()
	p := filepath.Join(e.home, "dict.wav")
	writeWAV(t, p, secs)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSidecarDictationHeldAndCancelled(t *testing.T) {
	e := fwEnv(t, "held")
	res, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), "")
	if err != nil || !res.Held || res.HeldReason != "suspect" || !strings.HasPrefix(res.Text, "Fw words of dictation-") {
		t.Fatalf("%+v %v", res, err)
	}

	// The caller goes away while the worker is busy: no answer, no whisper.cpp run in its place,
	// and the worker is stopped so that its late answer cannot reach the next job.
	e = fwEnv(t, "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if _, err := e.s.Dictate(ctx, wavBytes(t, e, 2), ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err: %v", err)
	}
	e.until("the worker to be stopped", func() bool { return !e.s.fw.running() })
	if len(e.runs()) != 0 {
		t.Fatal("whisper.cpp ran for a dictation nobody waits for")
	}
}

func TestSidecarLeavesWhenIdleAndComesBack(t *testing.T) {
	old := fwDefaultIdle
	fwDefaultIdle = 200 * time.Millisecond
	t.Cleanup(func() { fwDefaultIdle = old })
	e := fwEnv(t, "ok")
	for i := 0; i < 2; i++ {
		if _, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), ""); err != nil {
			t.Fatal(err)
		}
		e.until("the idle worker to leave (the GPU memory comes back)", func() bool { return !e.s.fw.running() })
	}
	if starts, jobs := e.fwRuns(); starts != 2 || len(jobs) != 2 {
		t.Fatalf("%d starts, %d jobs", starts, len(jobs))
	}
}

func TestSidecarToolsDirAndSettingsChangeRestart(t *testing.T) {
	e := fwEnv(t, "ok")
	if _, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), ""); err != nil {
		t.Fatal(err)
	}
	tools := filepath.Join(e.home, "voice-tools")
	os.MkdirAll(filepath.Join(tools, "cuda"), 0o700)
	e.mu.Lock()
	e.cfg.ToolsDir, e.cfg.Device = tools, "cpu"
	e.mu.Unlock()
	if spec, err := e.s.fwSpecNow(); err != nil || len(spec.DLLDirs) != 1 || spec.DLLDirs[0] != filepath.Join(tools, "cuda") {
		t.Fatalf("spec %+v %v", spec, err)
	}
	if _, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), ""); err != nil {
		t.Fatal(err)
	}
	starts, jobs := e.fwRuns()
	if starts != 2 || len(jobs) != 2 || jobs[1]["env_tools"] != tools || jobs[1]["env_device"] != "cpu" {
		t.Fatalf("%d starts, jobs %v", starts, jobs)
	}
}

func TestSidecarSetup(t *testing.T) {
	e := newEnv(t)
	if fwLockKey(e.s.o.GOOS, e.s.o.GOARCH) == "" {
		t.Skip("no hash-locked faster-whisper package list for " + e.s.platform())
	}
	e.mu.Lock()
	e.cfg.Engine = config.EngineFasterWhisper
	e.mu.Unlock()
	var steps []string
	oldExec, oldLook := fwExec, fwLookPath
	t.Cleanup(func() { fwExec, fwLookPath = oldExec, oldLook })
	base := filepath.Join(e.home, "base-python")
	fwLookPath = func(name string) (string, error) { return base, nil }
	fwExec = func(ctx context.Context, env []string, argv ...string) (string, error) {
		for _, kv := range env {
			if strings.HasPrefix(kv, "PYTHONPATH=") || strings.HasPrefix(kv, "HF_HUB_OFFLINE=") {
				t.Errorf("setup environment carries %s", kv)
			}
		}
		line := strings.Join(argv, " ")
		switch {
		case strings.Contains(line, "sys.executable"):
			return base + "\n", nil
		case len(argv) > 2 && argv[1] == "-m" && argv[2] == "venv":
			steps = append(steps, "venv")
			if argv[0] != base || argv[3] != e.s.fwVenv() {
				t.Errorf("venv: %v", argv)
			}
			os.MkdirAll(filepath.Dir(e.s.fwVenvPython()), 0o700)
			return "", os.WriteFile(e.s.fwVenvPython(), []byte("python"), 0o700)
		case len(argv) > 2 && argv[2] == "pip":
			lock := argv[len(argv)-1]
			steps = append(steps, "pip:"+filepath.Base(lock))
			b, err := os.ReadFile(lock)
			if argv[0] != e.s.fwVenvPython() || err != nil || !bytes.Contains(b, []byte("--hash=sha256:")) ||
				!strings.Contains(line, "--require-hashes --no-deps --only-binary :all: -r") {
				t.Errorf("pip: %v (%v)", argv, err)
			}
			return "", nil
		case strings.Contains(line, "get_cuda_device_count"):
			steps = append(steps, "probe")
			return "some warning\n1\n", nil
		case strings.Contains(line, "download_model"):
			for k, m := range fwModels {
				if strings.Contains(line, m.Repo) {
					steps = append(steps, "model:"+k)
					dir := filepath.Join(e.s.fwModelsDir(), m.Folder)
					if !strings.Contains(line, "revision='"+m.Revision+"'") || !strings.Contains(line, pyStr(dir)) {
						t.Errorf("download: %s", line)
					}
					os.MkdirAll(dir, 0o700)
					return "", os.WriteFile(filepath.Join(dir, "model.bin"), []byte("w"), 0o600)
				}
			}
		}
		t.Errorf("unexpected step: %v", argv)
		return "", errors.New("unexpected")
	}
	// The fake download writes "w" as each model's model.bin: pin that, as the real hashes pin
	// the real files.
	pinned := fwModels
	fwModels = map[string]fwModel{}
	for k, m := range pinned {
		m.Files = map[string]string{"model.bin": "50e721e49c013f00c62cf59f2163542a9d8df02464efeb615d31051b0fddc326"} // sha256("w")
		fwModels[k] = m
	}
	t.Cleanup(func() { fwModels = pinned })
	if err := e.s.setupSidecar(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"venv", "pip:" + fwLockKey(e.s.o.GOOS, e.s.o.GOARCH) + ".txt", "probe"}
	if e.s.o.GOOS == "windows" {
		want = append(want, "pip:cuda-win-x64.txt")
	}
	want = append(want, "model:turbo", "model:small")
	if strings.Join(steps, ",") != strings.Join(want, ",") {
		t.Fatalf("steps %v, want %v", steps, want)
	}
	spec, err := e.s.fwSpecNow()
	if err != nil || spec.Python != e.s.fwVenvPython() || filepath.Base(spec.Model) != fwTurbo || filepath.Base(spec.Fallback) != fwSmall {
		t.Fatalf("after the setup: %+v %v", spec, err)
	}
	if rev, _ := os.ReadFile(filepath.Join(spec.Model, fwRevisionFile)); strings.TrimSpace(string(rev)) != fwModels["turbo"].Revision {
		t.Fatalf("revision file: %q", rev)
	}
	// Again: nothing is made or downloaded twice (pip runs again: it is its own check).
	steps = nil
	if err := e.s.setupSidecar(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s == "venv" || strings.HasPrefix(s, "model:") {
			t.Fatalf("second setup repeated %v", steps)
		}
	}
	// A failing step is reported in the status.
	fwExec = func(ctx context.Context, env []string, argv ...string) (string, error) {
		return "", errors.New("exit status 1: ERROR: THESE PACKAGES DO NOT MATCH THE HASHES")
	}
	if err := e.s.setupSidecar(context.Background()); err == nil {
		t.Fatal("a failing pip passed")
	}
	if st := e.s.Status(); !strings.Contains(st.Sidecar.Error, "DO NOT MATCH THE HASHES") || st.Sidecar.Step != "" {
		t.Fatalf("status: %+v", st.Sidecar)
	}
}

func TestSidecarEmbeddedFiles(t *testing.T) {
	// What makes the worker Python tmx's transcriber: the same transcribe arguments.
	for _, want := range []string{`"vad_filter": False`, `"condition_on_previous_text": False`, `"beam_size": 5`, `req.get("initial_prompt")`,
		`kw["hotwords"] = req["hotwords"]`, `res["segments"]`} {
		if !bytes.Contains(workerScript, []byte(want)) {
			t.Errorf("the embedded worker lacks %s", want)
		}
	}
	for _, name := range []string{"win-x64.txt", "mac-arm64.txt", "linux-x64.txt", "cuda-win-x64.txt"} {
		b, err := lockFS.ReadFile("sidecar/locks/" + name)
		if err != nil {
			t.Fatal(err)
		}
		// Every requirement is pinned to a version and carries a hash.
		text := strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\\\n", " ")
		n := 0
		for _, l := range strings.Split(text, "\n") {
			if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			n++
			if !strings.Contains(l, "==") || !strings.Contains(l, "--hash=sha256:") {
				t.Errorf("%s: not pinned with a hash: %.60s", name, l)
			}
		}
		if n == 0 || (name != "cuda-win-x64.txt" && !strings.Contains(text, "faster-whisper==1.2.1")) {
			t.Errorf("%s: %d requirements", name, n)
		}
	}
	if fwLockKey("plan9", "amd64") != "" || fwLockKey("windows", "amd64") != "win-x64" {
		t.Fatal("lock keys")
	}
}

func TestDictationLanguageAllowlist(t *testing.T) {
	for _, tc := range []struct {
		name           string
		allowed        []string
		explicit, want string
		jobs           int
	}{
		{"default", nil, "", "en", 2},
		{"French first", []string{"fr", "en"}, "", "fr", 2},
		{"optional German", []string{"de", "en", "fr", "it"}, "", "de", 2},
		{"explicit overrides auto", nil, "it", "it", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := fwEnv(t, "outside")
			e.mu.Lock()
			e.cfg.DictationLanguages = tc.allowed
			e.mu.Unlock()
			res, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), tc.explicit)
			if err != nil || res.Language != tc.want {
				t.Fatalf("%+v %v", res, err)
			}
			starts, jobs := e.fwRuns()
			if starts != 1 || len(jobs) != tc.jobs {
				t.Fatalf("must retry on resident model: starts=%d jobs=%d", starts, len(jobs))
			}
			if tc.jobs == 2 && jobs[1]["language"] != tc.want {
				t.Fatalf("retry: %+v", jobs[1])
			}
		})
	}
}

func TestDictationLanguageConfig(t *testing.T) {
	e := newEnv(t)
	values := []string{"FR", "en", "fr", "de", "it"}
	status, err := e.s.Configure(SettingsPatch{DictationLanguages: &values})
	if err != nil || strings.Join(status.Settings.DictationLanguages, ",") != "fr,en,de,it" {
		t.Fatalf("%+v %v", status.Settings, err)
	}
	bad := []string{"auto"}
	if _, err := e.s.Configure(SettingsPatch{DictationLanguages: &bad}); err == nil {
		t.Fatal("auto is not an allowed detected language")
	}
}
