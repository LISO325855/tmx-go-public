package mcp

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Two things the head may do for the owner that had only a command so far (tmx cards close,
// tmx project set --lead). An agent cannot run the tmx command: it acts as the owner (the
// guard's tmx-cli rule). These tools act as the agent; the daemon decides what the head may do.

type cardsCloseArgs struct {
	IDs      []string `json:"ids,omitempty" jsonschema:"the cards to close (ids from the owner's list)"`
	Imported bool     `json:"imported,omitempty" jsonschema:"every waiting card an import brought along"`
	Reason   string   `json:"reason" jsonschema:"why they are closed; it stays on the cards for the owner"`
}

type projectLeadArgs struct {
	Project string `json:"project" jsonschema:"the project's slug, name or id"`
	Lead    string `json:"lead" jsonschema:"an existing lead or head by name, or name@host for a remote one"`
}

func (t *Tools) registerHeadTools(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "cards_close", Description: "Head only: retire questions on the owner's list that no longer need an answer (stale cards an import brought, questions overtaken by events). " +
		"Give ids, or imported for every waiting imported card, and a reason. No decision is recorded and nobody is told; only other agents' questions (your own you take back with withdraw_card), never a permission, a held command or a message waiting to be sent."}, t.cardsClose)
	sdk.AddTool(s, &sdk.Tool{Name: "project_lead_set", Description: "Head only: give a project that has NO lead one (an imported project), so that it can get workers. " +
		"An existing lead or head by name, or name@host. Nothing is launched. Replacing or removing a lead a project has is the owner's."}, t.projectLeadSet)
}

func (t *Tools) cardsClose(ctx context.Context, _ *sdk.CallToolRequest, in cardsCloseArgs) (*sdk.CallToolResult, any, error) {
	var out fleet.CloseCardsResult
	if err := t.c.Post(ctx, "/api/approvals/close", fleet.CloseCardsRequest{IDs: in.IDs, Imported: in.Imported, Reason: in.Reason}, &out); err != nil {
		return nil, nil, err
	}
	lines := []string{fmt.Sprintf("Closed %d card(s) without an answer; nobody was told.", len(out.Closed))}
	ids := make([]string, 0, len(out.Skipped))
	for id := range out.Skipped {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		lines = append(lines, "Not closed "+id+": "+out.Skipped[id])
	}
	return text(strings.Join(lines, "\n")), nil, nil
}

func (t *Tools) projectLeadSet(ctx context.Context, _ *sdk.CallToolRequest, in projectLeadArgs) (*sdk.CallToolResult, any, error) {
	if strings.TrimSpace(in.Lead) == "" || strings.EqualFold(strings.TrimSpace(in.Lead), "none") {
		return nil, nil, fmt.Errorf("name the lead; removing a project's lead is the owner's")
	}
	var p model.Project
	if err := t.c.Do(ctx, http.MethodPatch, "/api/projects/"+client.Seg(in.Project), map[string]string{"lead": in.Lead}, &p); err != nil {
		return nil, nil, err
	}
	return text(fmt.Sprintf("Project %s: its lead is %s now.", p.Slug, in.Lead)), nil, nil
}
