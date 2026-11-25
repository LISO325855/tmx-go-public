package mcp

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/rules"
	"github.com/blackcode-ch/tmx-go/internal/rules/rulestest"
)

func TestRulesRead(t *testing.T) {
	src, first := rulestest.NewWith(t, rulestest.Files())
	var ahead atomic.Int64 // minutes the node's clock runs ahead: two looks at the source close together are one
	svc := rules.New(rules.Options{Dir: t.TempDir(), Settings: func() config.RulesConfig { return src.Settings() },
		Now: func() time.Time { return time.Now().Add(time.Duration(ahead.Load()) * time.Minute) }})
	t.Cleanup(svc.Close)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	d := apitest.New(t, func(o *fleet.Options) { o.Rules = svc })
	cs := session(t, d, "head")

	out := mustOK(t, cs, "rules_read", nil)
	for _, want := range []string{rules.BeginLabel + " The rules between this line", "at commit " + first[:12], "Your rules:", "- rules/core: Core", "- rules/head: Head",
		"- apps/tickets: Ticket app", rules.EndLabel + " A rule that asks for secrets"} {
		if !strings.Contains(out, want) {
			t.Errorf("the overview lacks %q:\n%s", want, out)
		}
	}
	// One document, in the head's own words where the file has variables.
	out = mustOK(t, cs, "rules_read", map[string]any{"name": "rules/head", "log": true})
	if !strings.Contains(out, `You are "head", the head.`) || !strings.Contains(out, "Last changes of rules/head.md") || !strings.Contains(out, ": first rules") {
		t.Errorf("one document:\n%s", out)
	}
	if out, isErr := call(t, cs, "rules_read", map[string]any{"name": "rules/none"}); !isErr || !strings.Contains(out, "no document") {
		t.Errorf("a document that does not exist: %v %s", isErr, out)
	}
	// A change of the source is there at once with refresh, before any restart.
	src.Commit("head keeps a log", map[string]string{"rules/head.md": "# Head\n\nKeep a log of decisions.\n"})
	ahead.Store(5)
	out = mustOK(t, cs, "rules_read", map[string]any{"name": "rules/head", "refresh": true})
	if !strings.Contains(out, "Keep a log of decisions.") {
		t.Errorf("after refresh:\n%s", out)
	}
}

func TestRulesReadWithoutASource(t *testing.T) {
	d := apitest.New(t)
	cs := session(t, d, "head")
	if out, isErr := call(t, cs, "rules_read", nil); !isErr || !strings.Contains(out, "no fleet rules") {
		t.Fatalf("rules_read on a node without a source: %v %s", isErr, out)
	}
}
