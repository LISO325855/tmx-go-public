package transcribe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

func TestWorkerActivationRejectsNonPython(t *testing.T) {
	e := fwEnv(t, "ok")
	before := e.cfg
	st, err := e.s.Configure(SettingsPatch{Python: &e.cfg.Python})
	if !errors.Is(err, ErrInvalid) || st.Ready || !reflect.DeepEqual(before, e.cfg) || e.s.fw.running() {
		t.Fatalf("non-Python accepted or state changed: status=%+v err=%v", st, err)
	}
	// An existing zero-byte file is also rejected, not accepted as a Python fixture.
	empty := filepath.Join(t.TempDir(), "python.exe")
	if err := os.WriteFile(empty, nil, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Configure(SettingsPatch{Python: &empty}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty executable: %v", err)
	}
}

func TestPython3ProbeInstalledInterpreter(t *testing.T) {
	python := os.Getenv("TMXG_TEST_PYTHON")
	if python == "" {
		var err error
		python, err = exec.LookPath("python3")
		if err != nil {
			python, err = exec.LookPath("python")
		}
		if err != nil {
			t.Skip("no system Python interpreter")
		}
	} else if !filepath.IsAbs(python) {
		t.Fatal("TMXG_TEST_PYTHON must name an absolute interpreter path")
	}
	if err := probePython(context.Background(), fwSpec{Python: python, Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerActivationInitialGroupedSettings(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TMXG_FAKE_FW", "ok")
	t.Setenv("TMXG_FAKE_PYTHON", "3")
	python, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	model, tools := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(model, "model.bin"), []byte("weights"), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := e.s.Configure(SettingsPatch{Python: &python, ToolsDir: &tools, ModelDir: &model, Engine: ptr(config.EngineFasterWhisper)})
	if err != nil || !st.Ready || !e.s.fw.running() || e.cfg.ModelDir != model || e.cfg.ToolsDir != tools {
		t.Fatalf("initial grouped activation: %+v %v", st, err)
	}
}

func TestWorkerActivationPreservesOldOnFailureAndAdoptsSuccess(t *testing.T) {
	e := fwEnv(t, "ok")
	t.Setenv("TMXG_FAKE_PYTHON", "3")
	if _, err := e.s.Configure(SettingsPatch{Python: &e.cfg.Python, ModelDir: &e.cfg.ModelDir}); err != nil {
		t.Fatal(err)
	}
	oldPID := e.s.fw.cmd.Process.Pid
	before := e.cfg
	tools := t.TempDir()
	t.Setenv("TMXG_FAKE_FW", "fail")
	st, err := e.s.Configure(SettingsPatch{ToolsDir: &tools})
	if !errors.Is(err, ErrInvalid) || st.Ready || !reflect.DeepEqual(before, e.cfg) || !e.s.fw.running() || e.s.fw.cmd.Process.Pid != oldPID {
		t.Fatalf("failed activation changed state: %+v %v", st, err)
	}
	// The old process inherited 'ok'; it remains usable after the rejected candidate.
	spec, _ := e.s.fwSpecNow()
	answer, err := e.s.fw.do(context.Background(), spec, fwRequest{Warm: true}, time.Second, nil)
	if err != nil || !answer.OK {
		t.Fatalf("old worker unavailable: %+v %v", answer, err)
	}
	t.Setenv("TMXG_FAKE_FW", "ok")
	save := e.s.o.Save
	e.s.o.Save = func(config.VoiceConfig) error { return errors.New("disk write failed") }
	st, err = e.s.Configure(SettingsPatch{ToolsDir: &tools})
	if err == nil || st.Ready || !reflect.DeepEqual(before, e.cfg) || e.s.fw.cmd.Process.Pid != oldPID {
		t.Fatal("save failure changed old state")
	}
	e.s.o.Save = save
	oldExited := e.s.fw.exited
	if _, err := e.s.Configure(SettingsPatch{ToolsDir: &tools}); err != nil {
		t.Fatal(err)
	}
	if e.cfg.ToolsDir != tools || e.cfg.Python != before.Python || e.cfg.Prompt != before.Prompt || e.s.fw.cmd.Process.Pid == oldPID {
		t.Fatal("successful candidate was not adopted or unrelated fields changed")
	}
	select {
	case <-oldExited:
	default:
		t.Fatal("old process was not retired")
	}
	newPID := e.s.fw.cmd.Process.Pid
	spec, _ = e.s.fwSpecNow()
	answer, err = e.s.fw.do(context.Background(), spec, fwRequest{Warm: true}, time.Second, nil)
	if err != nil || !answer.OK || e.s.fw.cmd.Process.Pid != newPID {
		t.Fatal("adopted worker was not reused")
	}
	if _, err := e.s.Configure(SettingsPatch{ToolsDir: ptr("")}); err != nil {
		t.Fatal(err)
	}
	if e.cfg.ToolsDir != "" || e.cfg.ModelDir != before.ModelDir {
		t.Fatal("reset changed unrelated paths")
	}
}

func TestWorkerActivationProbeTimeout(t *testing.T) {
	e := fwEnv(t, "ok")
	t.Setenv("TMXG_FAKE_PYTHON", "slow")
	old := pythonProbeWait
	pythonProbeWait = 100 * time.Millisecond
	t.Cleanup(func() { pythonProbeWait = old })
	before := e.cfg
	st, err := e.s.Configure(SettingsPatch{Python: &e.cfg.Python})
	if err == nil || st.Ready || !reflect.DeepEqual(before, e.cfg) {
		t.Fatal("timeout committed settings")
	}
}

func TestWorkerActivationHealthTimeoutPreservesOld(t *testing.T) {
	e := fwEnv(t, "ok")
	t.Setenv("TMXG_FAKE_PYTHON", "3")
	if _, err := e.s.Configure(SettingsPatch{Python: &e.cfg.Python}); err != nil {
		t.Fatal(err)
	}
	before, pid := e.cfg, e.s.fw.cmd.Process.Pid
	t.Setenv("TMXG_FAKE_FW", "slowwarm")
	old := fwLoadWait
	fwLoadWait = 100 * time.Millisecond
	t.Cleanup(func() { fwLoadWait = old })
	st, err := e.s.Configure(SettingsPatch{Python: &e.cfg.Python})
	if err == nil || st.Ready || !reflect.DeepEqual(before, e.cfg) || !e.s.fw.running() || e.s.fw.cmd.Process.Pid != pid {
		t.Fatal("health timeout changed old state")
	}
}
