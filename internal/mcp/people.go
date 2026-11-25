package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// People for agents, under Go's own names (a Python tool name never means something else here):
// people lists who is in People; person_message proposes a message to one of them, which the
// owner approves before it is sent (or which is kept "to carry by hand"); promise_* keep asks
// and drafts as promises on the Promises page, where nothing is ever sent.

type personMessageArgs struct {
	Person string `json:"person" jsonschema:"the person's key (see the people tool)"`
	Text   string `json:"text" jsonschema:"the message as the owner would write it to that person (up to 16 KB); no credentials or links to join"`
	Title  string `json:"title,omitempty" jsonschema:"one line for the owner's Promises page, used when the message has to be carried by hand"`
}

type personProposeArgs struct {
	Person  string `json:"person" jsonschema:"the person's key (see the People page)"`
	Title   string `json:"title" jsonschema:"one line: what you ask or promise"`
	Draft   string `json:"draft,omitempty" jsonschema:"the message you propose to send; stored for the owner, not sent"`
	Channel string `json:"channel,omitempty" jsonschema:"whatsapp, email or github (a hint for the owner)"`
	Due     string `json:"due,omitempty" jsonschema:"ISO date to follow up (you get a reminder)"`
}

type personOutboxArgs struct {
	Status string `json:"status,omitempty" jsonschema:"open (default), done, dropped or all"`
}

type promiseIDArgs struct {
	ID string `json:"id" jsonschema:"the ask/draft id from promise_list"`
}

func (t *Tools) registerPeople(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "people", Description: "The people in the owner's People list: key, name, role, language, kind (team, external, client), preferred channel, " +
		"which channels exist (never a number or an address) and how many open asks each has. Read only."}, t.people)
	sdk.AddTool(s, &sdk.Tool{Name: "person_message", Description: "Propose a message to a person in the owner's People list. The owner always decides: " +
		"where the person has a verified WhatsApp chat on this machine, the owner gets a card with your exact text and Allow sends it (you hear the outcome by mail); " +
		"otherwise nothing can be sent from here and the draft is kept as an open promise for the owner to carry by hand. You never see or give a number."}, t.personMessage)
	sdk.AddTool(s, &sdk.Tool{Name: "promise_propose", Description: "Propose an ask or a message to a person without their own tmx. " +
		"Stored as an open promise on the Promises page for the owner; NOT sent â€” sending needs the owner's OK."}, t.personPropose)
	sdk.AddTool(s, &sdk.Tool{Name: "promise_list", Description: "Your asks and drafts to people (the promises you opened or are assigned)."}, t.personOutbox)
	sdk.AddTool(s, &sdk.Tool{Name: "promise_close", Description: "Close one of your asks as done."}, t.personAskClose)
	sdk.AddTool(s, &sdk.Tool{Name: "promise_drop", Description: "Withdraw one of your drafts (marks it dropped)."}, t.personDraftWithdraw)
}

func (t *Tools) people(ctx context.Context, _ *sdk.CallToolRequest, _ noArgs) (*sdk.CallToolResult, any, error) {
	var list []model.PublicPerson
	if err := t.c.Get(ctx, "/api/team", &list); err != nil {
		return nil, nil, err
	}
	if len(list) == 0 {
		return text("No people in the owner's People list yet."), nil, nil
	}
	lines := make([]string, 0, len(list))
	for _, p := range list {
		line := "- " + p.Key
		if p.Name != "" {
			line += ": " + p.Name
		}
		for _, v := range []string{p.Role, p.Kind, p.Lang} {
			if v != "" {
				line += ", " + v
			}
		}
		var ch []string
		if p.HasWhatsApp {
			ch = append(ch, "whatsapp")
		}
		if p.HasEmail {
			ch = append(ch, "email")
		}
		if p.GitHub != "" {
			ch = append(ch, "github")
		}
		if p.HasTmx {
			ch = append(ch, "tmx")
		}
		if len(ch) > 0 {
			line += "; channels: " + strings.Join(ch, ", ")
		}
		if p.Preferred != "" {
			line += "; prefers " + p.Preferred
		}
		if p.OpenPromises > 0 {
			line += fmt.Sprintf("; %d open ask(s)", p.OpenPromises)
		}
		lines = append(lines, line)
	}
	// Names and roles were typed by the owner or imported: data, never instructions.
	return untrusted(strings.Join(lines, "\n")), nil, nil
}

func (t *Tools) personMessage(ctx context.Context, _ *sdk.CallToolRequest, in personMessageArgs) (*sdk.CallToolResult, any, error) {
	if strings.TrimSpace(in.Person) == "" {
		return nil, nil, fmt.Errorf("name the person by their People key (see the people tool)")
	}
	var out struct {
		Via      string          `json:"via"`
		Person   string          `json:"person"`
		Approval *model.Approval `json:"approval"`
		Sent     bool            `json:"sent"`
		Promise  *model.Promise  `json:"promise"`
		Reason   string          `json:"reason"`
	}
	if err := t.c.Post(ctx, "/api/team/"+url.PathEscape(strings.ToLower(strings.TrimSpace(in.Person)))+"/message", map[string]string{"text": in.Text, "title": in.Title}, &out); err != nil {
		return nil, nil, err
	}
	if out.Via == "whatsapp" && out.Sent {
		return text(fmt.Sprintf("Sent to %s by WhatsApp (the owner lets you send without asking).", out.Person)), nil, nil
	}
	if out.Via == "whatsapp" && out.Approval != nil {
		return text(fmt.Sprintf("Waiting for the owner's OK (card %s): on Allow it is sent to %s by WhatsApp; you hear the outcome by mail. Nothing was sent yet. "+
			"If it is no longer needed, take it back with withdraw_card.", out.Approval.ID, out.Person)), nil, nil
	}
	id := ""
	if out.Promise != nil {
		id = out.Promise.ID
	}
	return text(fmt.Sprintf("Not sent, and it cannot be sent from here: %s. The draft is kept for the owner as promise %s (to carry by hand); see promise_list.", out.Reason, id)), nil, nil
}

func (t *Tools) personPropose(ctx context.Context, _ *sdk.CallToolRequest, in personProposeArgs) (*sdk.CallToolResult, any, error) {
	body := map[string]any{"person_key": in.Person, "title": in.Title}
	var txt []string
	if in.Channel != "" {
		txt = append(txt, "Channel: "+in.Channel)
	}
	if in.Draft != "" {
		txt = append(txt, "Draft (not sent; needs the owner's OK):\n"+in.Draft)
	}
	if len(txt) > 0 {
		body["text"] = strings.Join(txt, "\n")
	}
	if in.Due != "" {
		body["due"] = in.Due
	}
	var p model.Promise
	if err := t.c.Post(ctx, "/api/promises", body, &p); err != nil {
		return nil, nil, err
	}
	return text(fmt.Sprintf("Stored as %s (open) on the Promises page; nothing was sent.", p.ID)), nil, nil
}

func (t *Tools) personOutbox(ctx context.Context, _ *sdk.CallToolRequest, in personOutboxArgs) (*sdk.CallToolResult, any, error) {
	st := in.Status
	switch st {
	case "":
		st = "open"
	case "all":
		st = ""
	}
	var list []model.Promise
	if err := t.c.Get(ctx, "/api/promises?status="+url.QueryEscape(st), &list); err != nil {
		return nil, nil, err
	}
	if len(list) == 0 {
		return text("Nothing."), nil, nil
	}
	var b strings.Builder
	for _, p := range list {
		fmt.Fprintf(&b, "%s  %s  %s: %s", p.ID, p.Status, p.PersonKey, p.Title)
		if p.Due != "" {
			fmt.Fprintf(&b, " (due %s)", p.Due)
		}
		b.WriteByte('\n')
	}
	return text(b.String()), nil, nil
}

func (t *Tools) setPromiseStatus(ctx context.Context, id, status string) (*sdk.CallToolResult, any, error) {
	var p model.Promise
	if err := t.c.Post(ctx, "/api/promises/"+client.Seg(id), map[string]any{"status": status}, &p); err != nil {
		return nil, nil, err
	}
	return text(fmt.Sprintf("%s is %s.", p.ID, p.Status)), nil, nil
}

func (t *Tools) personAskClose(ctx context.Context, _ *sdk.CallToolRequest, in promiseIDArgs) (*sdk.CallToolResult, any, error) {
	return t.setPromiseStatus(ctx, in.ID, "done")
}

func (t *Tools) personDraftWithdraw(ctx context.Context, _ *sdk.CallToolRequest, in promiseIDArgs) (*sdk.CallToolResult, any, error) {
	return t.setPromiseStatus(ctx, in.ID, "dropped")
}
