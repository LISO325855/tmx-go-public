// The "An agent asks you" toasts (App.tsx): which cards get one and when one goes away.
// No runtime imports: also read by the node tests.
import type { Approval } from "./types";

/** A toast is shown only for a new, open request: not one put aside for later, not a record
 * that is already decided, and not a card of the owner's own import (those are listed in
 * Needs you without an alert each). An open card an agent brought in through an import is that
 * agent's question and gets its toast. The same rule as the phone alert (notifiesOwner). */
export function shouldToastApproval(a: Pick<Approval, "state" | "deferred" | "card">): boolean {
  if (a.state !== "pending" || a.deferred) return false;
  const origin = a.card?.origin;
  return !origin || origin.imported_by !== "owner";
}

export function approvalToastId(approvalId: string): string {
  return "approval:" + approvalId;
}

/** Of the cards that have a toast open, those whose cause is gone: answered, withdrawn or
 * expired cards are no longer among the pending ones. */
export function staleApprovalToasts(open: Iterable<string>, pending: Record<string, unknown>): string[] {
  return [...open].filter((id) => !(id in pending));
}
