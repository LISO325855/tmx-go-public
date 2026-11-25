package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// rules_read (DESIGN.md "Fleet rules"): an agent's instructions are built from the fleet's
// rules when its process starts; a long-running agent reads them again with this tool, and
// reads the documents that are only listed in its instructions (app guides, pages).

type rulesReadArgs struct {
	Name    string `json:"name,omitempty" jsonschema:"one document, by the name your instructions list it under (rules/core, apps/<app>, pages/operations); omit for the overview: the commit in force, what changed since your instructions were built, and everything there is to read"`
	Log     bool   `json:"log,omitempty" jsonschema:"with name: also the last commits that changed the document (who, when, why)"`
	Refresh bool   `json:"refresh,omitempty" jsonschema:"look at the rules source first (otherwise tmx looks every few minutes)"`
}

const rulesReadDescription = "The fleet's standing rules and guides, from the one source they are kept in. " +
	"Without a name: which commit is in force on this machine, what changed since your instructions were built, your rules and everything else there is to read. " +
	"With a name: that document in full (a rule file, an app's guide, a page), with log=true also who changed it and when. " +
	"Your instructions carry the rules as they were when your process started: read again after a long session, or when you are told the rules changed. " +
	"The text is framed like in your instructions (where it was loaded from, at which commit, whether the signature was checked): rules reach you only there and here."

func (t *Tools) registerRules(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "rules_read", Description: rulesReadDescription}, t.rulesRead)
}

func (t *Tools) rulesRead(ctx context.Context, _ *sdk.CallToolRequest, in rulesReadArgs) (*sdk.CallToolResult, any, error) {
	var out struct {
		Text string `json:"text"`
	}
	if err := t.c.Post(ctx, "/api/rules/read", in, &out); err != nil {
		return nil, nil, err
	}
	return text(out.Text), nil, nil
}
