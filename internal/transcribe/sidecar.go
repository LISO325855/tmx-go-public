package transcribe

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// The faster-whisper worker (config voice.engine = "faster-whisper"): Python tmx's transcriber,
// shipped inside this binary. ONE Python process stays up with the model loaded (on the GPU
// when there is one) and takes jobs as JSON lines on stdin, one at a time:
//
//	{"audio": PATH, "initial_prompt": "...", "hotwords": "...", "language": null}
//	-> {"ok": true, "text": "...", "language": "fr", "duration": 12.3, "segments": [...]}
//	or {"ok": false, "error": "..."}; a long recording first answers {"progress": S, "duration": S}
//	once per chunk.
//
// sidecar/voice_worker.py is Python tmx's tmx/voice_worker.py (same transcribe arguments, silence
// guard, hallucination hold and chunking) plus the "segments" field. The worker ends
// idle_unload_s after its last job, which frees the GPU memory. A job the worker cannot do
// (it does not start, stops, answers too late or with an error) runs through whisper.cpp when
// that is set up.

//go:embed sidecar/voice_worker.py
var workerScript []byte

//go:embed sidecar/locks/*.txt
var lockFS embed.FS

const (
	fwTurbo = "faster-whisper-large-v3-turbo"
	fwSmall = "faster-whisper-small"
	// fwModelPrefix starts a transcript's model name when the worker made it.
	fwModelPrefix = "faster-whisper/"
)

// How long the worker may take for its next line (Python tmx's voice.answer_timeout): on a GPU
// 30 s plus the audio's length, at most 3 minutes; on the CPU 60 s plus four times the length,
// at most 15 minutes. A long recording answers once per chunk of at most 10 minutes.
// Variables for tests.
var (
	fwGPUBase, fwGPUMax = 30 * time.Second, 180 * time.Second
	fwCPUBase, fwCPUMax = 60 * time.Second, 900 * time.Second
	fwStopWait          = 10 * time.Second
	// fwLoadWait: the least a job gets that finds no worker running. The process starts, checks
	// its DLLs and loads the model before it answers (about 50 s on a cold Windows machine).
	fwLoadWait    = 180 * time.Second
	fwDefaultIdle = 300 * time.Second
)

// fwSpec is everything the worker process is started with; a change restarts it.
type fwSpec struct {
	Python, Script, Dir string
	ToolsDir            string
	Model, Fallback     string
	Device, Compute     string
	FFmpeg              string
	DLLDirs             []string
	Idle                time.Duration
}

func (f fwSpec) key() string {
	return strings.Join([]string{f.Python, f.Script, f.Dir, f.ToolsDir, f.Model, f.Fallback, f.Device, f.Compute, f.FFmpeg,
		strings.Join(f.DLLDirs, string(os.PathListSeparator))}, "\x00")
}

// fwWorkerArgs is how the worker's Python is started.
func fwWorkerArgs(script string) []string { return []string{"-I", "-X", "utf8", "-u", script} }

func (f fwSpec) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		// Nothing of the daemon's environment may redirect the worker's Python (as the setup's
		// environment, fwSetupEnv): no PYTHONHOME, PYTHONPATH, PYTHONSTARTUP or any other
		// PYTHON* variable (-I ignores them too), and none of the worker's own settings.
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(strings.ToUpper(k), "PYTHON") || strings.HasPrefix(strings.ToUpper(k), "TMX_VOICE_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "TMX_VOICE_MODEL="+f.Model, "TMX_VOICE_FALLBACK="+f.Fallback, "TMX_VOICE_DEVICE="+f.Device,
		"TMX_VOICE_COMPUTE="+f.Compute, "PYTHONIOENCODING=utf-8", "TMX_VOICE_FFMPEG="+f.FFmpeg,
		"TMX_VOICE_DLL_DIRS="+strings.Join(f.DLLDirs, string(os.PathListSeparator)))
	if f.ToolsDir != "" {
		// The DLL folders inside the tools are checked against its manifest.json by the worker.
		env = append(env, "TMX_VOICE_TOOLS="+f.ToolsDir, "TMX_VOICE_PINNED="+strings.Join([]string{"cuda", "ctranslate2"}, string(os.PathListSeparator)))
	}
	return env
}

// ---- paths and settings

func (s *Service) fwDir() string       { return filepath.Join(s.voiceDir(), "fw") }
func (s *Service) fwVenv() string      { return filepath.Join(s.fwDir(), "venv") }
func (s *Service) fwModelsDir() string { return filepath.Join(s.fwDir(), "models") }
func (s *Service) fwScript() string    { return filepath.Join(s.fwDir(), "voice_worker.py") }

// fwVenvPython is the interpreter of the venv `tmx voice setup` makes.
func (s *Service) fwVenvPython() string {
	if s.o.GOOS == "windows" {
		return filepath.Join(s.fwVenv(), "Scripts", "python.exe")
	}
	return filepath.Join(s.fwVenv(), "bin", "python")
}

func (s *Service) fwOn() bool {
	return strings.EqualFold(strings.TrimSpace(s.o.Settings().Engine), config.EngineFasterWhisper)
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// fwModelReady: a CTranslate2 model folder with its weights.
func fwModelReady(dir string) bool { return isFile(filepath.Join(dir, "model.bin")) }

// fwSpecNow resolves config.json "voice" into what the worker starts with; the error says, for
// the owner, why the worker cannot run.
func (s *Service) fwSpecNow() (fwSpec, error) {
	return s.fwSpecFor(s.o.Settings())
}

func (s *Service) fwSpecFor(c config.VoiceConfig) (fwSpec, error) {
	f := fwSpec{Python: strings.TrimSpace(c.Python), Script: s.fwScript(), Dir: s.o.Paths.Home, ToolsDir: strings.TrimSpace(c.ToolsDir),
		Model: strings.TrimSpace(c.ModelDir), Device: strings.ToLower(strings.TrimSpace(c.Device)), Compute: strings.TrimSpace(c.ComputeType),
		Idle: time.Duration(c.IdleUnloadS) * time.Second}
	if f.Python == "" {
		f.Python = s.fwVenvPython()
		if !isFile(f.Python) {
			return f, errors.New("The faster-whisper worker is not installed yet: set it up in Settings → Voice (tmx voice setup).")
		}
	} else if !filepath.IsAbs(f.Python) || !isFile(f.Python) {
		return f, fmt.Errorf("voice.python (%s) is not a Python on this machine: give its full path.", f.Python)
	}
	if f.ToolsDir != "" && (!filepath.IsAbs(f.ToolsDir) || !isDir(f.ToolsDir)) {
		return f, fmt.Errorf("voice.tools_dir (%s) is not a folder on this machine.", f.ToolsDir)
	}
	if f.Model == "" {
		f.Model = filepath.Join(s.fwModelsDir(), fwTurbo)
		if !fwModelReady(f.Model) && fwModelReady(filepath.Join(s.fwModelsDir(), fwSmall)) {
			f.Model = filepath.Join(s.fwModelsDir(), fwSmall) // a machine without a GPU has only the small one
		}
	}
	if !filepath.IsAbs(f.Model) || !fwModelReady(f.Model) {
		return f, fmt.Errorf("The faster-whisper model is not there (%s): download it in Settings → Voice (tmx voice setup).", f.Model)
	}
	if small := filepath.Join(filepath.Dir(f.Model), fwSmall); small != f.Model && fwModelReady(small) {
		f.Fallback = small // tried when the model does not load
	}
	if f.Device == "" {
		f.Device = "auto"
	}
	if f.Compute == "" {
		f.Compute = "auto"
	}
	if f.Idle <= 0 {
		f.Idle = fwDefaultIdle
	}
	// Windows: the folders with cuBLAS/cuDNN, which the worker adds to its DLL search.
	var dlls []string
	if f.ToolsDir != "" {
		dlls = []string{filepath.Join(f.ToolsDir, "cuda"), filepath.Join(f.ToolsDir, "ctranslate2")}
	} else if s.o.GOOS == "windows" {
		site := filepath.Join(filepath.Dir(filepath.Dir(f.Python)), "Lib", "site-packages")
		dlls = []string{filepath.Join(site, "nvidia", "cublas", "bin"), filepath.Join(site, "nvidia", "cuda_runtime", "bin"), filepath.Join(site, "ctranslate2")}
	}
	for _, d := range dlls {
		if isDir(d) {
			f.DLLDirs = append(f.DLLDirs, d)
		}
	}
	f.FFmpeg = s.ffmpeg()
	return f, nil
}

// ffmpeg is ffmpeg's full path, looked up once per daemon ("" = the worker decodes with PyAV).
// The worker never searches for it itself.
func (s *Service) ffmpeg() string {
	s.ffmpegOnce.Do(func() {
		p, err := exec.LookPath("ffmpeg")
		if err != nil {
			return
		}
		if p, err = filepath.Abs(p); err != nil {
			return
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		s.ffmpegPath = p
	})
	return s.ffmpegPath
}

// writeWorkerScript puts the embedded worker where Python runs it, replacing a copy that differs.
func (s *Service) writeWorkerScript() error {
	p := s.fwScript()
	if b, err := os.ReadFile(p); err == nil && bytes.Equal(b, workerScript) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".new"
	if err := os.WriteFile(tmp, workerScript, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// SidecarStatus is Status.Sidecar: the faster-whisper worker, when voice.engine asks for it.
type SidecarStatus struct {
	Ready    bool   `json:"ready"`
	Reason   string `json:"reason,omitempty"` // not ready: why
	Python   string `json:"python,omitempty"`
	ModelDir string `json:"model_dir,omitempty"`
	Running  bool   `json:"running"`            // the worker process is up (the model is loaded or loading)
	Step     string `json:"step,omitempty"`     // the setup's step in progress
	Error    string `json:"error,omitempty"`    // the last setup's error
	Fallback bool   `json:"fallback,omitempty"` // whisper.cpp is set up and takes over when the worker fails
}

// ---- the worker process

type fwRequest struct {
	// Warm: load the model and answer, without any audio (the daemon's start, warm.go).
	Warm          bool    `json:"warm,omitempty"`
	Audio         string  `json:"audio"`
	InitialPrompt string  `json:"initial_prompt"`
	Hotwords      string  `json:"hotwords,omitempty"`
	Language      *string `json:"language"`
}

type fwAnswer struct {
	OK         bool    `json:"ok"`
	Error      string  `json:"error"`
	Text       string  `json:"text"`
	Language   *string `json:"language"`
	Duration   float64 `json:"duration"`
	Device     string  `json:"device"`
	Model      string  `json:"model"`
	Held       bool    `json:"held"`
	HeldReason string  `json:"held_reason"`
	Segments   []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

// fwWorker owns the one worker process. Jobs run one at a time (mu).
type fwWorker struct {
	log *slog.Logger

	mu     sync.Mutex
	closed bool
	spec   fwSpec
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan []byte   // the worker's answers; closed at its end of output
	quit   chan struct{} // closed when this process is given up
	exited chan struct{} // closed once it is gone
	stderr *lockedTail
	last   time.Time
	idle   *time.Timer
	starts int // processes started (tests)
}

type lockedTail struct {
	mu sync.Mutex
	t  tail
}

func (l *lockedTail) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t.Write(p)
}

func (l *lockedTail) explain() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t.explain()
}

func (w *fwWorker) running() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.aliveLocked()
}

func (w *fwWorker) aliveLocked() bool {
	if w.cmd == nil {
		return false
	}
	select {
	case <-w.exited:
		return false
	default:
		return true
	}
}

func (w *fwWorker) startLocked(spec fwSpec) error {
	// -I: isolated (no PYTHON* variables, no user site-packages, the script's folder not put on
	// sys.path); -X utf8 instead of PYTHONIOENCODING, which -I ignores; -u: unbuffered lines.
	cmd := hiddenexec.Command(spec.Python, fwWorkerArgs(spec.Script)...)
	cmd.Dir = spec.Dir // the state folder: never a folder whose DLLs or programs it could pick up
	cmd.Env = spec.env()
	hideWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	errTail := &lockedTail{t: tail{max: stderrKeep}}
	cmd.Stderr = errTail
	if err := cmd.Start(); err != nil {
		return err
	}
	lines, quit, exited := make(chan []byte, 4), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		r := bufio.NewReaderSize(stdout, 1<<16)
		for {
			line, err := r.ReadBytes('\n')
			if line = bytes.TrimSpace(line); len(line) > 0 {
				select {
				case lines <- line:
				case <-quit:
				}
			}
			if err != nil {
				break
			}
		}
		close(lines)
		_ = cmd.Wait()
	}()
	w.spec, w.cmd, w.stdin, w.lines, w.quit, w.exited, w.stderr = spec, cmd, stdin, lines, quit, exited, errTail
	w.starts++
	w.log.Info("voice: the faster-whisper worker runs", "pid", cmd.Process.Pid, "model", filepath.Base(spec.Model), "device", spec.Device)
	return nil
}

// stopLocked ends the process: politely (its input closes, it leaves) unless kill, then by force.
func (w *fwWorker) stopLocked(kill bool) {
	if w.cmd == nil {
		return
	}
	close(w.quit)
	_ = w.stdin.Close()
	if !kill {
		select {
		case <-w.exited:
		case <-time.After(fwStopWait):
			kill = true
		}
	}
	if kill {
		_ = w.cmd.Process.Kill() // our own child, by its handle
		<-w.exited
	}
	w.cmd, w.stdin, w.lines = nil, nil, nil
	if w.idle != nil {
		w.idle.Stop()
		w.idle = nil
	}
}

func (w *fwWorker) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.stopLocked(false)
}

// reap ends a worker nobody used for spec.Idle (a job in progress keeps it: mu is taken).
func (w *fwWorker) reap() {
	if !w.mu.TryLock() {
		return
	}
	defer w.mu.Unlock()
	if w.cmd != nil && time.Since(w.last) >= w.spec.Idle {
		w.log.Info("voice: the faster-whisper worker ends (idle)")
		w.stopLocked(false)
	}
}

// errWorker marks a failure of the worker itself (not of the caller's context): the job may
// run through whisper.cpp instead.
type errWorker struct{ msg string }

func (e errWorker) Error() string { return e.msg }

// do runs one job. wait is how long the worker may take for each line.
func (w *fwWorker) do(ctx context.Context, spec fwSpec, req fwRequest, wait time.Duration, progress func(done, total float64)) (fwAnswer, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fwAnswer{}, errors.New("the daemon is stopping")
	}
	if w.cmd != nil && (!w.aliveLocked() || w.spec.key() != spec.key()) {
		w.stopLocked(false) // it left, or the settings changed
	}
	if w.cmd == nil {
		if err := w.startLocked(spec); err != nil {
			return fwAnswer{}, errWorker{fmt.Sprintf("the faster-whisper worker does not start: %v", err)}
		}
		wait = max(wait, fwLoadWait)
	}
	w.spec.Idle = spec.Idle
	defer func() {
		w.last = time.Now()
		if w.cmd != nil {
			if w.idle != nil {
				w.idle.Stop()
			}
			w.idle = time.AfterFunc(w.spec.Idle+100*time.Millisecond, w.reap)
		}
	}()
	b, err := json.Marshal(req)
	if err != nil {
		return fwAnswer{}, err
	}
	if _, err := w.stdin.Write(append(b, '\n')); err != nil {
		why := w.stderr.explain()
		w.stopLocked(true)
		return fwAnswer{}, errWorker{"the faster-whisper worker stopped" + why}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-w.lines:
			if !ok {
				w.stopLocked(false)
				return fwAnswer{}, errWorker{"the faster-whisper worker stopped (is faster-whisper installed for voice.python?)" + w.stderr.explain()}
			}
			if bytes.HasPrefix(line, []byte(`{"progress"`)) { // a long recording: one line per chunk
				var p struct{ Progress, Duration float64 }
				if json.Unmarshal(line, &p) == nil && progress != nil {
					progress(p.Progress, p.Duration)
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(wait)
				continue
			}
			var a fwAnswer
			if err := json.Unmarshal(line, &a); err != nil {
				w.stopLocked(true) // what it says next cannot be trusted to belong to the next job
				return fwAnswer{}, errWorker{"the faster-whisper worker answered something unreadable"}
			}
			return a, nil
		case <-timer.C:
			w.stopLocked(true)
			return fwAnswer{}, errWorker{fmt.Sprintf("the faster-whisper worker did not answer within %s and was stopped", wait.Round(time.Second))}
		case <-ctx.Done():
			w.stopLocked(true) // its late answer must not reach the next job
			return fwAnswer{}, ctx.Err()
		}
	}
}

// fwWait is the time limit for the worker's next line on audio of about d.
func fwWait(device string, d time.Duration) time.Duration {
	if d > 10*time.Minute {
		d = 10 * time.Minute // a chunk
	}
	if device == "cpu" {
		return min(fwCPUMax, fwCPUBase+4*d)
	}
	return min(fwGPUMax, fwGPUBase+d)
}

// fwTranscribe runs a job through the worker. The model name says what did it.
func (s *Service) fwTranscribe(ctx context.Context, j *job, lang string) (Result, string, error) {
	s.fwUseMu.RLock()
	defer s.fwUseMu.RUnlock()
	name := fwModelPrefix + "?"
	spec, err := s.fwSpecNow()
	if err != nil {
		return Result{}, name, errWorker{lowerFirst(err.Error())}
	}
	name = fwModelPrefix + strings.TrimPrefix(filepath.Base(spec.Model), "faster-whisper-")
	if err := s.writeWorkerScript(); err != nil {
		return Result{}, name, errWorker{fmt.Sprintf("the faster-whisper worker's script cannot be written: %v", err)}
	}
	dur := j.duration
	if dur <= 0 {
		if w, err := readWAV(j.audio); err == nil {
			dur = w.Duration()
		} else if st, err := os.Stat(j.audio); err == nil {
			dur = time.Duration(st.Size()/2000) * time.Second // compressed audio: about 16 kbit/s at least
		}
	}
	c := s.o.Settings()
	req := fwRequest{Audio: j.audio, InitialPrompt: c.Prompt, Hotwords: strings.TrimSpace(c.Hotwords)}
	if lang != "" && lang != "auto" {
		req.Language = &lang
	}
	s.mu.Lock()
	j.status.Phase, j.status.Position = "transcribing", 0
	s.mu.Unlock()
	s.publish(true)
	a, err := s.fw.do(ctx, spec, req, fwWait(spec.Device, dur), nil)
	if err != nil {
		return Result{}, name, err
	}
	if !a.OK {
		return Result{}, name, errWorker{"the faster-whisper worker failed: " + a.Error}
	}
	if a.Model != "" {
		name = fwModelPrefix + strings.TrimPrefix(a.Model, "faster-whisper-")
	}
	r := Result{Segments: []model.TranscriptSegment{}, Held: a.Held, HeldReason: a.HeldReason}
	for _, seg := range a.Segments {
		if t := strings.Join(strings.Fields(seg.Text), " "); t != "" {
			r.Segments = append(r.Segments, model.TranscriptSegment{FromMS: int64(math.Round(seg.Start * 1000)), ToMS: int64(math.Round(seg.End * 1000)), Text: t})
		}
	}
	r.Text = strings.Join(strings.Fields(a.Text), " ")
	if len(r.Segments) == 0 && r.Text != "" { // a worker without the segments field
		r.Segments = append(r.Segments, model.TranscriptSegment{ToMS: int64(math.Round(a.Duration * 1000)), Text: r.Text})
	}
	if a.Language != nil {
		r.Language = *a.Language
	}
	if r.Language == "" && lang != "auto" {
		r.Language = lang
	}
	if a.Held {
		s.log.Info("voice: the worker marked a transcript as unsure", "reason", a.HeldReason) // never the text
	}
	return r, name, nil
}
