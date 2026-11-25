// Team status (DESIGN.md "Team status"): GET /api/team-status on each connection and whenever a
// team.updated event says a report or a group's team settings changed. The page, the sidebar's
// "Team" item, the opt-in in a group chat and Settings all read this one store.
import { create } from "zustand";
import { onBusEvent, useStore } from "./store";
import type { BusEvent } from "./types";
import type { TeamPage, TeamSharing } from "./team-types";

export * from "./team-types";

interface TeamState {
  page: TeamPage | null;
  loaded: boolean;
  /** the last GET failed (503: peer-to-peer off; 404: an older daemon) */
  error: { status: number; message: string } | null;
}

export const useTeam = create<TeamState>(() => ({ page: null, loaded: false, error: null }));

function transport() {
  const t = useStore.getState().transport;
  if (!t) throw new Error("Not connected yet.");
  return t;
}

let loading: Promise<void> | null = null;
let again = false;

export function loadTeam(): Promise<void> {
  if (loading) {
    again = true;
    return loading;
  }
  loading = (async () => {
    try {
      const page = await transport().request<TeamPage>("GET", "/api/team-status");
      useTeam.setState({ page, loaded: true, error: null });
    } catch (e) {
      const err = e as { status?: number; message?: string };
      useTeam.setState({ loaded: true, error: { status: err.status ?? 0, message: err.message || "The team could not be read." } });
    } finally {
      loading = null;
      if (again) {
        again = false;
        void loadTeam();
      }
    }
  })();
  return loading;
}

let timer: ReturnType<typeof setTimeout> | null = null;
onBusEvent((ev: BusEvent) => {
  if (ev.type === "snapshot") void loadTeam();
  if (ev.type === "team.updated" || ev.type === "chat.updated") {
    // Reports arrive in bursts (each member every 15 minutes, all at once after a restart).
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => void loadTeam(), 400);
  }
});

const enc = encodeURIComponent;

export const teamApi = {
  share: (chatId: string, share: boolean) =>
    transport()
      .request<TeamSharing>("PUT", `/api/team-status/${enc(chatId)}/share`, { share })
      .then((r) => (void loadTeam(), r)),
  open: (chatId: string, open: boolean) =>
    transport()
      .request<TeamSharing>("PUT", `/api/team-status/${enc(chatId)}/open`, { open })
      .then((r) => (void loadTeam(), r)),
};

/** The opt-in for one group, when it is to be offered (undefined otherwise). */
export function useShareOffer(chatId: string): TeamSharing | undefined {
  return useTeam((s) => s.page?.sharing.find((x) => x.chat_id === chatId && !x.admin && x.collects && x.share === "unasked"));
}
