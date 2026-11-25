/** An update may reload only after every active send has settled. */
export class ReloadGate {
  private holds = 0;
  private reload: (() => void) | undefined;
  private listeners = new Set<() => void>();
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };
  pending = () => !!this.reload;
  private notify() { for (const fn of this.listeners) fn(); }
  hold(): () => void {
    this.holds++;
    let released = false;
    return () => { if (released) return; released = true; this.holds--; this.flush(); };
  }
  request(reload: () => void) { this.reload = reload; this.flush(); }
  private flush() {
    if (!this.holds && this.reload) { const reload = this.reload; this.reload = undefined; reload(); }
    this.notify();
  }
}
export const reloadGate = new ReloadGate();
