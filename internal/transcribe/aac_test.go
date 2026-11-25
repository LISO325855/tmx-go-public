package transcribe

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

func TestAudioKindAAC(t *testing.T) {
	for _, tc := range []struct {
		head []byte
		want string
	}{
		{[]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}, "m4a"},
		{[]byte{0xff, 0xf1, 0x50, 0x80}, "aac"}, {[]byte{0xff, 0xf9, 0x50, 0x80}, "aac"},
		{[]byte{0xff, 0xfb, 0x90, 0}, "mp3"}, {[]byte{0, 0, 0, 0, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}, ""},
	} {
		if got := audioKind(tc.head); got != tc.want {
			t.Fatalf("%x: %s, want %s", tc.head, got, tc.want)
		}
	}
}
func TestTranscribeAACRequiresFFmpeg(t *testing.T) {
	e := newEnv(t)
	e.s.ffmpegOnce.Do(func() {})
	_, err := e.s.TranscribeFile(context.Background(), bytes.NewReader([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}), "")
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("%v", err)
	}
}
func TestTranscribeGeneratedAAC(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg required to generate AAC fixtures")
	}
	e := newEnv(t)
	for _, ext := range []string{"m4a", "aac"} {
		file := filepath.Join(t.TempDir(), "sample."+ext)
		cmd := hiddenexec.CommandContext(context.Background(), ff, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "aac", file)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generate: %v %s", err, out)
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if audioKind(b) != ext {
			t.Fatalf("bad kind: %s", audioKind(b))
		}
		r, err := e.s.TranscribeFile(context.Background(), bytes.NewReader(b), "")
		if err != nil || !strings.Contains(r.Text, ".wav") {
			t.Fatalf("%s: %+v %v", ext, r, err)
		}
	}
}
