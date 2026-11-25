package api_test

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api"
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
)

// The goroutine dump is the owner's, read on the machine: it names what goroutines wait on.
func TestGoroutineDumpIsTheOwnersOnly(t *testing.T) {
	d := apitest.New(t)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	head := client{t: t, base: d.URL, token: d.AgentToken(t, "head")}
	var dump api.GoroutineDump
	if s := owner.do("GET", "/api/debug/goroutines", nil, &dump); s != 200 || dump.Goroutines < 1 || dump.At == "" ||
		!strings.Contains(dump.Dump, "goroutine ") || !strings.Contains(dump.Dump, "net/http") {
		t.Fatalf("the owner's dump: %d %d goroutines, %d bytes", s, dump.Goroutines, len(dump.Dump))
	}
	if s := head.do("GET", "/api/debug/goroutines", nil, nil); s != 403 {
		t.Fatalf("an agent read the dump: %d", s)
	}
	anon := client{t: t, base: d.URL}
	if s := anon.do("GET", "/api/debug/goroutines", nil, nil); s != 401 && s != 403 {
		t.Fatalf("no token: %d", s)
	}
}
