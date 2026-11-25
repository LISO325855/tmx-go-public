import { reloadGate } from "./reload-guard.ts";
/** A send may finish after typing or transcription has changed the draft. */
export function clearSubmittedDraft(drafts: Map<string, string>, key: string, submitted: string): boolean {
  if ((drafts.get(key) ?? "") !== submitted) return false;
  drafts.delete(key);
  return true;
}
/** Agent drafts survive an automatic UI update in this tab. */
export class ReloadSafeDrafts extends Map<string, string> {
  private storageKey: string;
  private releaseReload?: () => void;
  private blockReload: boolean;
  constructor(storageKey: string, blockReload = true) {
    super();
    this.storageKey = storageKey;
    this.blockReload = blockReload;
    try {
      const rows: unknown = JSON.parse(sessionStorage.getItem(storageKey) ?? "[]");
      if (Array.isArray(rows)) for (const row of rows) {
        if (Array.isArray(row) && typeof row[0] === "string" && typeof row[1] === "string") super.set(row[0], row[1]);
      }
    } catch { /* a new or storage-restricted tab */ }
    this.guardReload();
  }
  private guardReload() {
    const dirty = this.blockReload && [...this.values()].some(value => value.length > 0);
    if (dirty && !this.releaseReload) this.releaseReload = reloadGate.hold();
    if (!dirty && this.releaseReload) { const release = this.releaseReload; this.releaseReload = undefined; release(); }
  }
  private save() { this.guardReload(); try { sessionStorage.setItem(this.storageKey, JSON.stringify([...this])); } catch { /* retain in memory */ } }
  override set(key: string, value: string) { super.set(key, value); this.save(); return this; }
  override delete(key: string) { const deleted = super.delete(key); this.save(); return deleted; }
  override clear() { super.clear(); this.save(); }
}
/** Keep the same request identity after a reload, including an uncertain HTTP outcome. */
export class ReloadSafeSends {
  private saved: ReloadSafeDrafts;
  constructor(storageKey: string) { this.saved = new ReloadSafeDrafts(storageKey, false); }
  get(key: string): { envelope: string; id: string } | undefined {
    try {
      const value = JSON.parse(this.saved.get(key) ?? "null");
      if (value && typeof value.envelope === "string" && typeof value.id === "string") return value;
    } catch { /* invalid saved value is not a request */ }
    return undefined;
  }
  set(key: string, value: { envelope: string; id: string }) { this.saved.set(key, JSON.stringify(value)); }
  delete(key: string) { return this.saved.delete(key); }
}
