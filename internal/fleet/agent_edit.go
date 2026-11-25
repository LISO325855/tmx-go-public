package fleet

import (
	"reflect"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// AgentPatch is PATCH /api/agents/{id}; nil fields stay.
type AgentPatch struct {
	Protected *bool   `json:"protected,omitempty"`
	Role      *string `json:"role,omitempty"`
	Brief     *string `json:"brief,omitempty"`
	Model     *string `json:"model,omitempty"`   // "" = the profile's default
	Profile   *string `json:"profile,omitempty"` // a profile of the agent's level
}

// AgentUpdate is the answer to an AgentPatch. Changes reach a running agent only when it
// restarts: its instructions, model and tools are fixed when its process starts.
type AgentUpdate struct {
	Agent         model.Agent `json:"agent"`
	Changed       bool        `json:"changed"`
	RestartNeeded bool        `json:"restart_needed"`
	Note          string      `json:"note"`
}

// UpdateAgent changes an agent's role, brief, model or profile: the owner, the head, or a lead
// for an agent of its own project (manage.go). The model is
// checked against the (new) profile; a new profile without a model gives its default model.
func (f *Fleet) UpdateAgent(c Caller, idOrName string, p AgentPatch) (AgentUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.resolveLocked(idOrName)
	if err != nil {
		return AgentUpdate{}, err
	}
	if err := f.manageAgentLocked(c, r, "edit an agent"); err != nil {
		return AgentUpdate{}, err
	}
	// A shell's brief is the command it runs: changing it is the owner's (shell_start.go).
	if !c.Owner && r.a.Harness == model.HarnessShell {
		return AgentUpdate{}, errf(KindForbidden, "only the owner can edit a shell: its brief is the command it runs")
	}
	next := r.a
	prof := f.profileForLocked(next)
	if p.Profile != nil && *p.Profile != next.Profile {
		np, ok := f.profiles[*p.Profile]
		if !ok {
			return AgentUpdate{}, errf(KindInvalid, "no profile %q", *p.Profile)
		}
		if np.Level != next.Level {
			return AgentUpdate{}, errf(KindInvalid, "%s is a %s and keeps its level; the profile %s is for %s agents",
				next.Name, next.Level, np.Slug, np.Level)
		}
		if prof.Provider != np.Provider {
			return AgentUpdate{}, errf(KindInvalid, "launch a new agent to change providers")
		}
		if err := harnessProfileOK(next.Harness, *np); err != nil {
			return AgentUpdate{}, err
		}
		prof, next.Profile = *np, np.Slug
		if p.Model == nil && next.Harness == model.HarnessClaude {
			next.Model = prof.Model
		}
	}
	if p.Model != nil {
		if next.Model, err = f.pickHarnessModelLocked(next.Harness, prof, *p.Model, true); err != nil {
			return AgentUpdate{}, err
		}
		if next.Harness == model.HarnessShell && *p.Model != "" {
			return AgentUpdate{}, errf(KindInvalid, "a shell has no model")
		}
	}
	if p.Role != nil {
		next.Role = *p.Role
	}
	if p.Brief != nil {
		next.Brief = *p.Brief
	}
	if p.Protected != nil {
		next.Protected = *p.Protected
	}

	out := AgentUpdate{Changed: !reflect.DeepEqual(next, r.a)}
	restartNeeded := next.Role != r.a.Role || next.Brief != r.a.Brief || next.Model != r.a.Model || next.Profile != r.a.Profile
	if out.Changed {
		r.a = next
		f.saveAgentLocked(r)
	}
	out.Agent = r.a
	running := r.proc != nil || r.starting
	switch {
	case !out.Changed:
		out.Note = "Nothing changed."
	case running && restartNeeded:
		out.RestartNeeded = true
		out.Note = "Saved; applies on restart."
	case !restartNeeded:
		out.Note = "Saved."
	default:
		out.Note = "Saved; applies when the agent starts."
	}
	return out, nil
}
