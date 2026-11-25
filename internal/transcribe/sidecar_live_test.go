package transcribe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// The real faster-whisper worker through the service, timed; CI never sets TMXG_LIVE_FW.
//
//	TMXG_LIVE_FW=1 TMXG_FW_PYTHON=/path/python TMXG_FW_MODEL=/path/faster-whisper-large-v3-turbo \
//	  TMXG_FW_AUDIO=/path/clip.wav [TMXG_FW_TOOLS=/path/voice-tools] [TMXG_FW_PROMPT=...] [TMXG_FW_HOTWORDS=...] \
//	  [TMXG_FW_EXPECT="a phrase"] [TMXG_FW_RUNS=4] go test -run LiveSidecar -v ./internal/transcribe/
//
// The first run loads the model; the others show the latency of a resident worker. The home is
// a throwaway folder: nothing of TMXG_FW_PYTHON/TOOLS/MODEL is written.
func TestLiveSidecar(t *testing.T) {
	if os.Getenv("TMXG_LIVE_FW") != "1" {
		t.Skip("set TMXG_LIVE_FW=1 to run the real faster-whisper worker")
	}
	cfg := config.VoiceConfig{Engine: config.EngineFasterWhisper, Python: os.Getenv("TMXG_FW_PYTHON"), ToolsDir: os.Getenv("TMXG_FW_TOOLS"),
		ModelDir: os.Getenv("TMXG_FW_MODEL"), Prompt: os.Getenv("TMXG_FW_PROMPT"), Hotwords: os.Getenv("TMXG_FW_HOTWORDS")}
	s := New(Options{Paths: config.Paths{Home: t.TempDir()}, GOOS: "plan9", // no whisper.cpp here: a worker failure fails the test
		Settings: func() config.VoiceConfig { return cfg }})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if st := s.Status(); !st.Ready {
		t.Fatalf("not ready: %s", st.Reason)
	}
	runs := 4
	if n := os.Getenv("TMXG_FW_RUNS"); n != "" {
		runs = int(n[0] - '0')
	}
	// TMXG_FW_AUDIO may list several clips (separated like PATH): each runs `runs` times.
	for _, clip := range filepath.SplitList(os.Getenv("TMXG_FW_AUDIO")) {
		for i := 1; i <= runs; i++ {
			f, err := os.Open(clip)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			res, err := s.TranscribeFile(context.Background(), f, "")
			took := time.Since(start)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s run %d: %.3f s, language %s, %d segments, held %v: %s", filepath.Base(clip), i, took.Seconds(), res.Language, len(res.Segments), res.Held, res.Text)
			if want := os.Getenv("TMXG_FW_EXPECT"); want != "" && !strings.Contains(normalized(res.Text), normalized(want)) {
				t.Fatalf("the text lacks %q", want)
			}
		}
	}
	if !s.fw.running() {
		t.Fatal("the worker is not resident after its jobs")
	}
}
