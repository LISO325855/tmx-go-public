import { create } from "zustand";

const KEY = "tmx.showAgentMessages";

function savedPreference(): boolean {
  try {
    return localStorage.getItem(KEY) === "1";
  } catch {
    return false;
  }
}

export const useAgentMessagePreference = create<{
  show: boolean;
  setShow: (show: boolean) => void;
}>((set) => ({
  show: savedPreference(),
  setShow(show) {
    set({ show });
    try {
      if (show) localStorage.setItem(KEY, "1");
      else localStorage.removeItem(KEY);
    } catch {
      // Keep the preference for this visit when storage is unavailable.
    }
  },
}));
