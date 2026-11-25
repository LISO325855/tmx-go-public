// What the pinned-messages strip shows (no imports: also read by the node tests).

// The strip takes no room at all while there is nothing to show: no pin of any state (active
// or done) and no sync error to report.
export function pinsStripVisible(pinCount: number, error: string): boolean {
  return pinCount > 0 || error !== "";
}
