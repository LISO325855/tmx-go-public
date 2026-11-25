package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/client"
)

// Exercise the advertised schema and SDK validation, not only the Go handler.
// The Board reads these same thread cards through the owner's API.
func TestMatrixProgressSchemaAndBoard(t *testing.T) {
	d := apitest.New(t)
	cs := session(t, d, "head")
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range listed.Tools {
		if tool.Name != "matrix" {
			continue
		}
		found = true
		b, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(b, &schema); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"pct", "blocker", "why"} {
			if _, ok := schema.Properties[field]; !ok {
				t.Fatalf("matrix schema missing %s: %s", field, b)
			}
		}
	}
	if !found {
		t.Fatal("matrix tool missing")
	}
	mustOK(t, cs, "matrix", map[string]any{"op": "new", "thread": "Board regression"})
	owner := client.New(d.URL, d.OwnerToken)
	for _, pct := range []int{0, 43, 100} {
		blocker := "Review needed"
		if pct == 100 {
			blocker = ""
		}
		mustOK(t, cs, "matrix", map[string]any{"op": "state", "thread": "Board regression", "pct": pct, "why": "Verified step", "blocker": blocker})
		var cards []threadCard
		if err := owner.Get(context.Background(), "/api/matrix/mine", &cards); err != nil {
			t.Fatal(err)
		}
		seen := false
		for _, card := range cards {
			th := card.Thread
			if th.Title != "Board regression" {
				continue
			}
			seen = true
			if th.Progress == nil || *th.Progress != pct || th.ProgressWhy != "Verified step" || th.Blocker != blocker {
				t.Fatalf("Board did not receive report: %+v", th)
			}
		}
		if !seen {
			t.Fatal("Board thread missing")
		}
	}
	for _, pct := range []int{-1, 101} {
		if _, bad := call(t, cs, "matrix", map[string]any{"op": "state", "thread": "Board regression", "pct": pct}); !bad {
			t.Fatalf("accepted pct %d", pct)
		}
	}
}
