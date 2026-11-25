package fleet

import (
	"encoding/json"

	"github.com/blackcode-ch/tmx-go/internal/guard"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"time"
)

// P2PApprovalTTL gives the owner time to reach the phone. Execution receipts live longer.
const P2PApprovalTTL = time.Hour

const ToolP2PInvite = "p2p_invite_create"
const ToolP2PJoin = "p2p_join"

// ToolP2PInviteWA creates a single-use person invite and sends it to that person's verified
// WhatsApp number in one daemon-owned step (api/p2p_invite_wa.go); the agent never sees the link.
const ToolP2PInviteWA = "p2p_invite_whatsapp"

// IsP2PTool reports whether tool is one of the daemon-held pairing tools.
func IsP2PTool(tool string) bool {
	return tool == ToolP2PInvite || tool == ToolP2PJoin || tool == ToolP2PInviteWA
}

// CheckP2PAgent checks the authenticated local agent, never a supplied name or tier.
func (f *Fleet) CheckP2PAgent(c Caller) error {
	return f.checkP2P(c, true)
}

// CheckP2PAction revalidates a daemon-held action independently of agent liveness.
func (f *Fleet) CheckP2PAction(c Caller) error {
	return f.checkP2P(c, false)
}

func (f *Fleet) checkP2P(c Caller, live bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.callerRecLocked(c)
	if err != nil {
		return err
	}
	if c.Owner || c.Peer != "" || r == nil || (r.a.Level != model.LevelHead && r.a.Level != model.LevelLead) {
		return errf(KindForbidden, "only head and lead agents may use pairing tools")
	}
	if (live && (r.proc == nil || r.stopping || r.a.Desired == "stopped")) || f.profileForLocked(r.a).ReadOnly {
		return errf(KindForbidden, "pairing requires a running, writable head or lead")
	}
	return nil
}

// AnswerRule is the Approval.Answer of a pairing call that ran because its guard rule is Allowed.
const AnswerRule = "allowed by rule"

// HoldP2P applies the pairing tool's guard rule (switchable since 17 Nov, Allowed by default).
// Allowed: an approval that is already allowed, with a row in the agent's timeline, and no
// card. Blocked: refused. Held for me: a card; it shares phone approvals and Safety's revocable
// standing grants. Command is the tool name deliberately: Allow always covers this tool, not a
// past link. These rules are NOT Rememberable: Allow once never becomes Allow always.
func (f *Fleet) HoldP2P(c Caller, tool, description string) (model.Approval, error) {
	if err := f.CheckP2PAgent(c); err != nil {
		return model.Approval{}, err
	}
	var rule string
	switch tool {
	case ToolP2PInvite:
		rule = guard.RuleP2PInvite
	case ToolP2PJoin:
		rule = guard.RuleP2PJoin
	case ToolP2PInviteWA:
		rule = guard.RuleP2PInviteWA
	default:
		return model.Approval{}, errf(KindInvalid, "unknown pairing tool")
	}
	req := HoldRequest{Tool: tool, Command: tool, Rule: rule, Detail: description, Match: description}
	switch f.ruleStateOf(rule) {
	case guard.Hold:
		return f.hold(c, req)
	case guard.Block:
		return model.Approval{}, errf(KindForbidden, "the owner's guard rule blocks this pairing tool")
	}
	r, _ := guard.RuleByID(rule)
	info := model.GuardHold{Outcome: string(guard.Allow), Rule: r.ID, RuleName: r.Name, Why: r.Why,
		Detail: description, Match: description, Command: tool, Tool: tool, NoAlways: true}
	raw, _ := json.Marshal(info)
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, err := f.callerRecLocked(c)
	if err != nil {
		return model.Approval{}, err
	}
	if rec == nil {
		return model.Approval{}, errf(KindForbidden, "only head and lead agents may use pairing tools")
	}
	ap := model.Approval{ID: model.NewID(), AgentID: rec.a.ID, Kind: model.ApprovalGuard, Tool: tool,
		Input: raw, Description: description, Options: []string{}, State: model.ApprovalAllowed,
		Answer: AnswerRule, CreatedAt: model.Now()}
	if err := f.st.PutApproval(ap); err != nil {
		return model.Approval{}, err
	}
	f.appendLocked(rec, model.TimelineEntry{Kind: model.EntrySystem, Tool: tool,
		Text: "Allowed by your guard rule: " + r.Name + " (" + description + ")",
		Meta: map[string]any{"guard": info, "approval_id": ap.ID, "decision": string(model.ApprovalAllowed)}})
	return ap, nil
}
