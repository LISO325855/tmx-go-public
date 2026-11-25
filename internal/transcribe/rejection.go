package transcribe

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
)

func dictationRejectReason(err error) string {
	switch {
	case errors.Is(err, ErrNoSpeech):
		return "no_speech"
	case errors.Is(err, ErrInvalid):
		if strings.Contains(err.Error(), "at most") {
			return "too_long"
		}
		if strings.Contains(err.Error(), "language") {
			return "invalid_language"
		}
		return "invalid_audio"
	default:
		return "transcription_failed"
	}
}

// Diagnostics contain only levels and counts, never the audio or transcript.
func (s *Service) logDictationRejected(audio []byte, w wavInfo, reason, language string, chars int) {
	rms, peak := pcmLevels(audio, w)
	s.log.Info("voice: dictation rejected or held", "duration_s", w.Duration().Seconds(), "rms", rms, "peak", peak, "reason", reason, "language", language, "chars", chars)
}

func pcmLevels(audio []byte, w wavInfo) (rms, peak float64) {
	if !w.pcm16() || w.DataOffset < 0 || w.DataSize < 2 || w.DataOffset > int64(len(audio)) || w.DataSize > int64(len(audio))-w.DataOffset {
		return 0, 0
	}
	data := audio[w.DataOffset : w.DataOffset+w.DataSize]
	var sum float64
	for i := 0; i+1 < len(data); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(data[i:]))) / 32768
		sum += v * v
		peak = math.Max(peak, math.Abs(v))
	}
	return math.Sqrt(sum / float64(len(data)/2)), peak
}
