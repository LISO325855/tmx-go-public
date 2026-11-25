export const STREAM_SILENCE_MS = 45_000;
export const HEALTH_INTERVAL_MS = 30_000;

/** No content is inspected: a frame proves the stream is alive; HTTP alone does not. */
export class StreamHealth {
  lastFrameAt = 0;
  private version = "";
  private reloading = false;
  frame(now: number) { this.lastFrameAt = now; }
  stale(now: number) { return now - this.lastFrameAt >= STREAM_SILENCE_MS; }
  versionChanged(version: string): boolean {
    if (!version || this.reloading) return false;
    if (!this.version) { this.version = version; return false; }
    if (this.version === version) return false;
    this.reloading = true;
    return true;
  }
}

export async function bounded<T>(work: Promise<T>, ms = 8000): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([work, new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error("Connection timed out")), ms);
    })]);
  } finally { clearTimeout(timer); }
}
