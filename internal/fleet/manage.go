package fleet

import "github.com/blackcode-ch/tmx-go/internal/model"

// Who manages the fleet (the owner, 17 Nov 22:18: "The head can do everything; a lead can do them
// inside its own project."). Until then removing or editing an agent, its mode, people, projects,
// profiles, the policy and pins were the owner's alone, and each refusal sent an agent back to
// him. Now:
//
//   - the owner: everything, as before;
//   - the head: the same, node-wide;
//   - a lead: the same inside a project it leads (its agents, the project itself);
//   - workers: none of it (they do the work);
//   - a paired machine and Companion: none of it.
//
// What stays the owner's alone is not decided here: his cards and menus, the guard's switches
// and gate, his keyboard in a terminal or shell, acting as him.

// managerLocked returns the calling agent when it is the head or a lead; nil with no error for
// the owner. what completes the refusal ("only the owner, the head or a project's lead can
// <what>").
func (f *Fleet) managerLocked(c Caller, what string) (*agentRec, error) {
	if c.Owner {
		return nil, nil
	}
	refuse := errf(KindForbidden, "only the owner, the head or a project's lead can %s", what)
	if c.AgentID == "" || c != AgentCaller(c.AgentID) {
		return nil, refuse // a paired machine, Companion
	}
	me, ok := f.agents[c.AgentID]
	if !ok {
		return nil, errf(KindForbidden, "the calling agent no longer exists")
	}
	if me.a.Level != model.LevelHead && me.a.Level != model.LevelLead {
		return nil, refuse
	}
	return me, nil
}

// manageNodeLocked: something that belongs to the whole node (profiles, the policy, people): the
// owner or the head.
func (f *Fleet) manageNodeLocked(c Caller, what string) error {
	me, err := f.managerLocked(c, what)
	if err != nil {
		return err
	}
	if me != nil && !isHead(me) {
		return errf(KindForbidden, "only the owner or the head can %s", what)
	}
	return nil
}

// leadsProjectLocked reports whether me leads the project with this ID: it is the project's
// lead, or a lead that belongs to it.
func (f *Fleet) leadsProjectLocked(me *agentRec, projectID string) bool {
	if me == nil || projectID == "" || me.a.Level != model.LevelLead {
		return false
	}
	p := f.projects[projectID]
	return p != nil && (p.LeadID == me.a.ID || me.a.ProjectID == p.ID)
}

// manageProjectLocked: the owner, the head, or a lead of that project.
func (f *Fleet) manageProjectLocked(c Caller, p *model.Project, what string) (*agentRec, error) {
	me, err := f.managerLocked(c, what)
	if err != nil || me == nil || isHead(me) {
		return me, err
	}
	if !f.leadsProjectLocked(me, p.ID) {
		return nil, errf(KindForbidden, "a lead can %s only in its own project (%s is not %s's)", what, p.Name, me.a.Name)
	}
	return me, nil
}

// manageAgentLocked: the owner, the head, or a lead for an agent of a project it leads or an
// agent below it in the tree. Never an agent on itself, and a lead never on the head.
func (f *Fleet) manageAgentLocked(c Caller, target *agentRec, what string) error {
	me, err := f.managerLocked(c, what)
	if err != nil || me == nil {
		return err
	}
	if me == target {
		return errf(KindForbidden, "an agent cannot do this to itself")
	}
	if isHead(me) {
		return nil
	}
	if isHead(target) || !(f.leadsProjectLocked(me, target.a.ProjectID) || f.isDescendantLocked(target, me.a.ID)) {
		return errf(KindForbidden, "a lead can %s only in its own project (%s is not in %s's)", what, target.a.Name, me.a.Name)
	}
	return nil
}

// isHeadCaller reports whether c is this machine's head agent.
func (f *Fleet) isHeadCaller(c Caller) bool {
	if c.AgentID == "" || c != AgentCaller(c.AgentID) {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return isHead(f.agents[c.AgentID])
}

// managePin: pins are the owner's and the head's; a lead pins for an agent of its own project.
// agentID "" (a pin of nobody's conversation, or a change of state) is the owner's or the head's.
func (f *Fleet) managePin(c Caller, agentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if agentID == "" {
		return f.manageNodeLocked(c, "change pins")
	}
	r, err := f.resolveLocked(agentID)
	if err != nil {
		return err
	}
	me, err := f.managerLocked(c, "pin messages")
	if err != nil || me == nil || isHead(me) || me == r {
		return err
	}
	return f.manageAgentLocked(c, r, "pin messages")
}

// IsManager reports whether c is the owner, the head or a lead: who may read the state of the
// node's links (Companion's).
func (f *Fleet) IsManager(c Caller) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := f.managerLocked(c, "read this")
	return err == nil
}

// SeesAgent reports whether c may read what the agent with this ID did (its Companion tasks and
// messages): the owner, the agent itself, the head, and a lead of the agent's project.
func (f *Fleet) SeesAgent(c Caller, agentID string) bool {
	if c.Owner || agentID != "" && c == AgentCaller(agentID) {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	me, err := f.managerLocked(c, "read this")
	if err != nil || me == nil {
		return err == nil
	}
	if isHead(me) {
		return true
	}
	target := f.agents[agentID]
	return target != nil && !isHead(target) && f.leadsProjectLocked(me, target.a.ProjectID)
}
