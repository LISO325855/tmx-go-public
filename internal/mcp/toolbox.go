package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/toolbox"
)

// Resources for agents (docs/RESOURCES.md): the same list the owner sees on the Resources page.
// Read only here; asking to use one (resource_use) and correcting a line (resource_set) come
// with the use log.

type resourcesArgs struct {
	Kind  string `json:"kind,omitempty" jsonschema:"only this kind: communication, tool, account, device or storage"`
	State string `json:"state,omitempty" jsonschema:"only this state: works, partly, planned, not_verified, not_connected; or down (its health check fails)"`
}

type resourceArgs struct {
	ID string `json:"id" jsonschema:"the resource's id, as resources lists it (e.g. agent-phone)"`
}

func (t *Tools) registerToolbox(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "resources", Description: "Everything agents can reach to act in the world, one line each: channels (WhatsApp, mail, SMS, voice), " +
		"tools (Companion, computer use), accounts, devices and storage, with its state and who holds it. The same list the owner sees on the Resources page. " +
		"Look here before you ask how to reach something. Read only; resource shows one in full."}, t.resources)
	sdk.AddTool(s, &sdk.Tool{Name: "resource", Description: "One resource in full: what it is for, who holds it, each action with its route (a tool to call, " +
		"the holder to mail, or none yet), the rule for using it, its state and health. Never a number, an address or a key."}, t.resource)
}

func (t *Tools) resources(ctx context.Context, _ *sdk.CallToolRequest, in resourcesArgs) (*sdk.CallToolResult, any, error) {
	q := url.Values{}
	if in.Kind != "" {
		q.Set("kind", in.Kind)
	}
	if in.State != "" {
		q.Set("state", in.State)
	}
	path := "/api/toolbox"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var list toolbox.List
	if err := t.c.Get(ctx, path, &list); err != nil {
		return nil, nil, err
	}
	return text(formatResources(list)), nil, nil
}

func (t *Tools) resource(ctx context.Context, _ *sdk.CallToolRequest, in resourceArgs) (*sdk.CallToolResult, any, error) {
	if strings.TrimSpace(in.ID) == "" {
		return nil, nil, fmt.Errorf("id is required (resources lists them)")
	}
	var r toolbox.Resource
	if err := t.c.Get(ctx, "/api/toolbox/"+client.Seg(strings.TrimSpace(in.ID)), &r); err != nil {
		return nil, nil, err
	}
	return text(formatResource(r)), nil, nil
}

var stateWords = map[string]string{
	toolbox.StateWorks: "works", toolbox.StatePartly: "partly", toolbox.StatePlanned: "planned", toolbox.StateNotVerified: "not verified",
	toolbox.StateNotConnected: "not connected yet",
}

// stateLine: "works", "partly; DOWN: no account is signed in".
func stateLine(r toolbox.Resource) string {
	s := stateWords[r.State]
	if s == "" {
		s = r.State
	}
	if h := r.Health; h != nil && h.Level != toolbox.HealthGood {
		s += "; " + strings.ToUpper(h.Level) + ": " + h.Reason
	}
	return s
}

func holderLine(r toolbox.Resource) string {
	switch {
	case r.Holder != "":
		return r.Holder
	case r.Machine != "":
		return r.Machine
	case r.HolderNote != "":
		return r.HolderNote
	}
	return "-"
}

// holderDot restores the full stop of a holder note that Trim took off.
func holderDot(r toolbox.Resource) string {
	if strings.HasSuffix(strings.TrimSpace(r.HolderNote), ".") {
		return "."
	}
	return ""
}

func formatResources(l toolbox.List) string {
	if len(l.Resources) == 0 {
		return "No resources match."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d resources, as %s sees them. id | name | state | held by. Use resource <id> for the actions and the rule.\n", len(l.Resources), l.Machine)
	kind := ""
	for _, r := range l.Resources {
		if r.Kind != kind {
			kind = r.Kind
			fmt.Fprintf(&b, "\n%s\n", strings.ToUpper(kind))
		}
		fmt.Fprintf(&b, "- %s | %s | %s | %s", r.ID, r.Name, stateLine(r), holderLine(r))
		if r.Meter != nil {
			fmt.Fprintf(&b, " | %s: %s", r.Meter.Label, r.Meter.Text)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func routeLine(rt toolbox.Route) string {
	switch rt.Kind {
	case toolbox.RouteTool:
		return "tool " + rt.Tool
	case toolbox.RouteMail:
		return "mail to " + rt.To
	}
	return "no route yet"
}

var actionWords = map[string]string{
	toolbox.ActionRead: "read", toolbox.ActionSendFleet: "send as the fleet", toolbox.ActionSendOwner: "send as the owner", toolbox.ActionAct: "act",
}

func formatResource(r toolbox.Resource) string {
	var b strings.Builder
	line := func(label, v string) {
		if strings.TrimSpace(v) != "" {
			fmt.Fprintf(&b, "%s: %s\n", label, v)
		}
	}
	fmt.Fprintf(&b, "%s (%s, %s)\n", r.Name, r.ID, r.Kind)
	line("What it is for", r.Summary)
	state := stateWords[r.State] + "."
	if r.StateNote != "" {
		state += " " + r.StateNote
	}
	if r.VerifiedBy != "" {
		state += " Checked by " + r.VerifiedBy
		if r.VerifiedAt != "" {
			state += " on " + r.VerifiedAt
		}
		state += "."
	}
	if r.VerifiedBy == "" && r.State == toolbox.StateNotVerified && r.StateNote == "" {
		state += " Nobody confirmed it."
	}
	line("State", state)
	if h := r.Health; h != nil {
		line("Health", h.Level+" at "+time.UnixMilli(h.CheckedAt).UTC().Format("2006-01-02 15:04")+" UTC. "+h.Reason)
	}
	if r.Meter != nil {
		line("What is left", r.Meter.Label+": "+r.Meter.Text)
	}
	who := r.Identity.Label
	if who == "" && r.Identity.Who != "none" {
		who = r.Identity.Who
	}
	line("Whose face it shows", who)
	line("Machine", r.Machine)
	line("Held by", strings.Trim(r.Holder+". "+r.HolderNote, ". ")+holderDot(r))
	line("Ask when broken", r.Maintainer)
	line("Depends on", r.Parent)
	line("How to use it", r.How)
	if len(r.Actions) > 0 {
		b.WriteString("Actions:\n")
		for _, a := range r.Actions {
			fmt.Fprintf(&b, "- %s (%s): %s", a.Name, actionWords[a.Type], routeLine(a.Route))
			if a.Note != "" {
				b.WriteString(". " + a.Note)
			}
			b.WriteByte('\n')
		}
	} else {
		b.WriteString("Actions: none for agents.\n")
	}
	rule := r.Rule.Text
	if r.Rule.ID != "" {
		rule += " (rule " + r.Rule.ID + ")"
	}
	line("Rule", rule)
	if len(r.Secrets) > 0 {
		var names []string
		for _, s := range r.Secrets {
			n := s.Name
			if s.Stored != nil {
				if *s.Stored {
					n += " (stored)"
				} else {
					n += " (missing)"
				}
			}
			names = append(names, n)
		}
		line("Secrets it needs (names only)", strings.Join(names, ", "))
	}
	src := "declared (written down, not measured by this node)"
	if r.Source == toolbox.SourceLive {
		src = "live (measured by this node)"
	}
	line("Source", src)
	return strings.TrimRight(b.String(), "\n")
}
