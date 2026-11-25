package cli

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// A detached Windows GUI process can have an unusable inherited stderr.
// Treat the file as the log of record and mirror it to stderr when available.
type daemonLogWriter struct {
	file, stderr   io.Writer
	stderrFailures atomic.Uint64
}

func (w *daemonLogWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	if err != nil {
		return n, err
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	if n, err := w.stderr.Write(p); err != nil || n != len(p) {
		if err == nil {
			err = io.ErrShortWrite
		}
		failures := w.stderrFailures.Add(1)
		if failures&(failures-1) == 0 {
			// Report the first failure and subsequent powers of two in the file.
			// The record already succeeded; a failed diagnostic must not change that.
			_, _ = fmt.Fprintf(w.file, "time=%s level=WARN msg=%q failures=%d err=%q\n",
				time.Now().Format(time.RFC3339Nano), "daemon stderr mirror write failed", failures, err.Error())
		}
	}
	return n, nil
}
