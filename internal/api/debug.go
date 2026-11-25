package api

import (
	"bytes"
	"net/http"
	"runtime"
	"runtime/pprof"
	"time"
)

// GET /api/debug/goroutines: what every goroutine of the daemon is doing right now, for the
// moment something hangs (a stop that does not come back, a route that never answers). The
// owner only, and only from this machine: a dump shows the daemon's inner workings, never
// message texts or secrets (stacks carry function names and addresses, not values), but it is
// nobody else's business. `tmx debug goroutines` saves it to a file.

// GoroutineDump is the answer.
type GoroutineDump struct {
	At         string `json:"at"`
	Goroutines int    `json:"goroutines"`
	Dump       string `json:"dump"` // pprof's text form, one block per goroutine with its wait reason and time
}

// maxGoroutineDump bounds the answer; a daemon with more than this in stacks is cut, and says so.
const maxGoroutineDump = 8 << 20

func (s *Server) debugGoroutines(w http.ResponseWriter, r *http.Request) {
	if !localHost(r.Host) {
		writeError(w, http.StatusForbidden, "the goroutine dump is read on the machine itself")
		return
	}
	var buf bytes.Buffer
	if p := pprof.Lookup("goroutine"); p != nil {
		_ = p.WriteTo(&buf, 2)
	}
	dump := buf.Bytes()
	if len(dump) > maxGoroutineDump {
		dump = append(dump[:maxGoroutineDump:maxGoroutineDump], []byte("\n[cut by tmx: the dump is longer]\n")...)
	}
	writeJSON(w, http.StatusOK, GoroutineDump{At: time.Now().UTC().Format(time.RFC3339), Goroutines: runtime.NumGoroutine(), Dump: string(dump)})
}
