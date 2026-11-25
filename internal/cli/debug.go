package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/client"
)

// tmx debug goroutines [--out FILE]: what the daemon's goroutines are doing right now, for the
// moment something hangs. Printed, or written to FILE.
func cmdDebug(e *env, args []string) error {
	const usage = "usage: tmx debug goroutines [--out FILE]"
	if len(args) == 0 || args[0] != "goroutines" {
		return usageErr(usage)
	}
	fs := newFlags("debug goroutines")
	out := fs.String("out", "", "write the dump to this file instead of printing it")
	if pos, err := parse(fs, args[1:]); err != nil {
		return err
	} else if len(pos) != 0 {
		return usageErr(usage)
	}
	return withOwner(e, func(c *client.Client, cx context.Context) error {
		var d api.GoroutineDump
		if err := c.Get(cx, "/api/debug/goroutines", &d); err != nil {
			return err
		}
		if *out == "" {
			fmt.Fprintf(e.out, "%s: %d goroutines\n\n%s", d.At, d.Goroutines, d.Dump)
			return nil
		}
		if err := os.WriteFile(*out, []byte(d.Dump), 0o600); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "%d goroutines at %s written to %s\n", d.Goroutines, d.At, *out)
		return nil
	})
}
