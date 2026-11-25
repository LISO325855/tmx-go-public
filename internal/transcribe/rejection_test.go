package transcribe

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestDictationRejectionMetadata(t *testing.T) {
	e := newEnv(t)
	var logs bytes.Buffer
	e.s.log = slog.New(slog.NewJSONHandler(&logs, nil))
	audio := append(wavHeader(1, 16000, 16, 64000), make([]byte, 64000)...)
	cases := []struct {
		name         string
		audio        []byte
		lang, reason string
	}{
		{"silent", audio, "", "no_speech"},
		{"invalid", []byte("bad WAV"), "", "invalid_audio"},
		{"language", audio, "invalid", "invalid_language"},
		{"long", append(wavHeader(1, 16000, 16, 4100000), make([]byte, 4100000)...), "", "too_long"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			if _, err := e.s.Dictate(context.Background(), tc.audio, tc.lang); err == nil {
				t.Fatal("expected rejection")
			}
			var row map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &row); err != nil {
				t.Fatal(err, logs.String())
			}
			for _, key := range []string{"duration_s", "rms", "peak", "reason", "language", "chars"} {
				if _, ok := row[key]; !ok {
					t.Fatal("missing", key)
				}
			}
			if row["reason"] != tc.reason {
				t.Fatal(row)
			}
			if row["level"] != "INFO" {
				t.Fatal(row)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(e.home, "voice", "held")); !os.IsNotExist(err) {
		t.Fatal("no held audio directory", err)
	}
}
func TestDictationPCMLevels(t *testing.T) {
	audio := append(wavHeader(1, 16000, 16, 8), make([]byte, 8)...)
	for i, v := range []int16{16384, -16384, 0, 0} {
		binary.LittleEndian.PutUint16(audio[44+i*2:], uint16(v))
	}
	w, err := parseWAV(bytes.NewReader(audio), int64(len(audio)))
	if err != nil {
		t.Fatal(err)
	}
	rms, peak := pcmLevels(audio, w)
	if math.Abs(rms-math.Sqrt(.125)) > 1e-9 || peak != .5 {
		t.Fatal(rms, peak)
	}
}

func TestDictationSuspectMetadataOnly(t *testing.T) {
	e := fwEnv(t, "held")
	var logs bytes.Buffer
	e.s.log = slog.New(slog.NewJSONHandler(&logs, nil))
	res, err := e.s.Dictate(context.Background(), wavBytes(t, e, 2), "en")
	if err != nil || !res.Held {
		t.Fatal(res, err)
	}
	var row map[string]any
	lines := bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n"))
	if err := json.Unmarshal(lines[len(lines)-1], &row); err != nil {
		t.Fatal(err, logs.String())
	}
	if row["reason"] != "suspect" || row["language"] != "en" || row["chars"] != float64(len([]rune(res.Text))) {
		t.Fatal(row)
	}
	if bytes.Contains(logs.Bytes(), []byte(res.Text)) {
		t.Fatal("transcript must not appear in log")
	}
	if _, err := os.Stat(filepath.Join(e.home, "voice", "held")); !os.IsNotExist(err) {
		t.Fatal("must not save held WAV", err)
	}
}
