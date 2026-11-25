package api

import (
	"mime"
	"net/http"
)

// Paired machines may forward owner-admitted microphone captures, never relayed
// agent requests. Keep this separate from owner-only voice settings and file uploads.
func (s *Server) peerDictate(w http.ResponseWriter, r *http.Request) {
	if peerOf(r).name == "" || relayedForOther(r) {
		writeError(w, http.StatusForbidden, "dictation requires the paired machine or its operator")
		return
	}
	kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if kind != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "dictation requires a JSON WAV capture")
		return
	}
	r.Header.Set(transcriptionLocalOnly, "true")
	// No durable upload or receipt is created; the ordinary request-scoped path
	// releases temporary audio on completion/cancellation and uses the shared worker.
	s.withVoice(s.transcribeNow)(w, r)
}
