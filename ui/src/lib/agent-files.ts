// Downloading a file from an agent's folder (GET /api/agents/{id}/download?path=…; the daemon
// keeps the path inside that agent's folder: internal/api/agent_files.go). The request carries
// the owner's token, so it is a fetch and not a plain link.
import { useStore } from "./store";

/** The address of the download, relative to the daemon. */
export function agentDownloadPath(agentId: string, path: string): string {
  return `/api/agents/${encodeURIComponent(agentId)}/download?path=${encodeURIComponent(path)}`;
}

/** Whether this window can download at all (a browser talking HTTP to the daemon; not the mock). */
export function canDownload(): boolean {
  return useStore.getState().transport?.kind === "http";
}

/** Fetches the file and hands it to the browser to save under its own name. Throws with a plain reason. */
export async function downloadAgentFile(agentId: string, path: string, name: string): Promise<void> {
  const t = useStore.getState().transport;
  if (!t || t.kind !== "http") throw new Error("Downloading works in the browser. Copy the path instead.");
  let res: Response;
  try {
    res = await fetch(t.baseURL + agentDownloadPath(agentId, path), { headers: t.token ? { Authorization: `Bearer ${t.token}` } : {} });
  } catch {
    throw new Error("The daemon is not reachable.");
  }
  if (!res.ok) {
    let msg = "";
    try {
      msg = ((await res.json()) as { error?: string }).error ?? "";
    } catch {
      /* not JSON */
    }
    throw new Error(msg || (res.status === 404 ? "That file is not there (yet)." : `The download failed (${res.status}).`));
  }
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement("a");
  a.href = url;
  a.download = name || "download";
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 30_000);
}
