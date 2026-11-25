package transcribe

import (
	"errors"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// Warming the faster-whisper worker, and one log line per transcription.
//
// The worker loads its model with its first request, which takes 50 to 65 seconds on a GPU
// machine: after every start of the daemon the owner's first dictation waited that long. With
// voice.engine = faster-whisper the daemon now starts the worker right away and has it load the
// model (a "warm" request, no audio), in the background: a dictation that comes meanwhile
// simply waits for the same load instead of starting its own.
//
// The worker still ends after voice.idle_unload_s without a job (default 300 s, to free the
// GPU's memory); the dictation after such a pause loads the model again. An owner who wants it
// resident sets idle_unload_s high in config.json.

// warmWorker starts the worker and loads its model, if faster-whisper is the engine and is set
// up. It never fails the daemon's start: whatever goes wrong is logged, and the first job
// starts the worker the usual way.
func (s *Service) warmWorker() {
	if !s.fwOn() || s.remoteURL() != "" {
		return // Fae is tried first; the local worker loads only if that job needs it
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.fwUseMu.RLock()
		defer s.fwUseMu.RUnlock()
		spec, err := s.fwSpecNow()
		if err != nil {
			s.log.Info("voice: the faster-whisper worker is not warmed at start", "why", err.Error())
			return
		}
		if err := s.writeWorkerScript(); err != nil {
			s.log.Warn("voice: the worker's script cannot be written", "err", err)
			return
		}
		began := time.Now()
		a, err := s.fw.do(s.ctx, spec, fwRequest{Warm: true}, fwLoadWait, nil)
		switch {
		case err != nil:
			if s.ctx.Err() == nil {
				s.log.Warn("voice: warming the faster-whisper worker failed; the first dictation starts it", "err", err)
			}
		case !a.OK:
			s.log.Warn("voice: the faster-whisper worker did not load its model at start", "err", a.Error)
		default:
			s.log.Info("voice: the faster-whisper worker is warm", "model", a.Model, "device", a.Device, "took_s", seconds(time.Since(began)))
		}
	}()
}

// logJob writes the one line a transcription leaves in the daemon's log: which engine did it,
// how long the audio was, how long it took and which language was heard. Never the text.
func (s *Service) logJob(j *job, r Result, modelName string, took time.Duration, err error) {
	engine := "whisper.cpp"
	switch {
	case j.peer != "":
		engine = "peer:" + j.peer
	case strings.HasPrefix(modelName, fwModelPrefix):
		engine = config.EngineFasterWhisper
	case strings.HasPrefix(modelName, "fae/"):
		engine = "fae"
	}
	audio := j.duration
	if audio <= 0 && len(r.Segments) > 0 {
		audio = time.Duration(r.Segments[len(r.Segments)-1].ToMS) * time.Millisecond
	}
	args := []any{"engine", engine, "model", modelName, "source", jobKind(j.source), "audio_s", seconds(audio), "took_s", seconds(took)}
	if r.Note != "" {
		args = append(args, "note", r.Note)
	}
	if err != nil {
		if errors.Is(err, ErrPending) || s.ctx.Err() != nil {
			return // handed to a peer, or the daemon is stopping: not a result
		}
		s.log.Warn("voice: transcription failed", append(args, "err", err.Error())...)
		return
	}
	lang := r.Language
	if lang == "" {
		lang = "?"
	}
	s.log.Info("voice: transcribed", append(args, "language", lang, "chars", len(r.Text), "held", r.Held)...)
}

// jobKind is a job's source without what identifies it ("recording:<id>" is "recording").
func jobKind(source string) string {
	kind, _, _ := strings.Cut(source, ":")
	if kind == "" {
		return "dictation"
	}
	return kind
}

// seconds rounds a duration to tenths of a second, for the log.
func seconds(d time.Duration) float64 {
	return float64(d.Round(100*time.Millisecond)) / float64(time.Second)
}
