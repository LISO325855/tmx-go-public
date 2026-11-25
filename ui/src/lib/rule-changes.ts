// Guard rule switches chosen in Settings > Safety but not saved yet. A click on a rule's switch
// only chooses; "Save" sends all choices together, so ONE confirmation (PIN or Windows Hello)
// covers the whole batch instead of one per rule.

import { create } from "zustand";
import type { GuardRule, RuleState } from "./types";

type State = { staged: Record<string, RuleState> };

export const useRuleChanges = create<State>(() => ({ staged: {} }));

/** The state a rule has on the daemon. */
export function savedState(r: GuardRule): RuleState {
  return (r.state ?? r.default ?? r.outcome) as RuleState;
}

/** Chooses next for rule r; choosing what is saved already takes the rule out of the batch. */
export function stageRule(r: GuardRule, next: RuleState) {
  useRuleChanges.setState((s) => stagedWith(s.staged, r, next));
}

export function stagedWith(staged: Record<string, RuleState>, r: GuardRule, next: RuleState): State {
  const out = { ...staged };
  if (next === savedState(r)) delete out[r.id];
  else out[r.id] = next;
  return { staged: out };
}

export function clearRuleChanges() {
  useRuleChanges.setState({ staged: {} });
}

/** The choices that still differ from what the daemon has (a rule changed elsewhere drops out). */
export function pendingChanges(staged: Record<string, RuleState>, rules: GuardRule[]): { rule: GuardRule; next: RuleState }[] {
  return rules.filter((r) => staged[r.id] !== undefined && staged[r.id] !== savedState(r) && r.switchable !== false).map((r) => ({ rule: r, next: staged[r.id] }));
}
