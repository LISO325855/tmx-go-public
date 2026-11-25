// Fleet rules (DESIGN.md "Fleet rules"): the daemon reads the fleet's standing rules from one
// git source and opens every agent's instructions with them; the UI shows which commit is in
// force, the documents, and which agents still run on older rules.
import { create } from "zustand";
import { ownerAction, withConfirmation } from "./owner-gate";
import { onBusEvent, useStore } from "./store";
import type { BusEvent } from "./types";
import type { RulesDoc, RulesView } from "./fleet-rules-format";

export const useFleetRules = create<{ view: RulesView | null }>(() => ({ view: null }));

function t() {
  const tr = useStore.getState().transport;
  if (!tr) throw new Error("Not connected yet.");
  return tr;
}

function keep(v: RulesView) {
  useFleetRules.setState({ view: v });
  return v;
}

export const fleetRulesApi = {
  get: () => t().request<RulesView>("GET", "/api/rules").then(keep),
  /** Looks at the source now; what the look found is in the view. */
  refresh: () => t().request<RulesView>("POST", "/api/rules/refresh").then(keep),
  doc: (name: string) => t().request<RulesDoc>("GET", `/api/rules/doc?name=${encodeURIComponent(name)}`),
  // Holding the node at a commit (or following the branch again) is behind the owner gate.
  hold: (commit: string) =>
    ownerAction((c) => t().request<RulesView>("PUT", withConfirmation("/api/rules/config", c), { pin: commit })).then(keep),
};

/** Loads the view once connected (a daemon without the route answers 404: the card stays away). */
export async function loadFleetRules() {
  try {
    await fleetRulesApi.get();
  } catch {
    useFleetRules.setState({ view: null });
  }
}

onBusEvent((ev: BusEvent) => {
  if (ev.type === "rules.changed") void loadFleetRules();
});
