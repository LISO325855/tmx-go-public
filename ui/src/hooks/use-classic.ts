import { isClassic } from "@/lib/ui-profile";
import { useStore } from "@/lib/store";

/** The classic UI profile is on for this daemon (lib/ui-profile.ts): be the Python dashboard. */
export function useClassic(): boolean {
  return useStore((s) => isClassic(s.uiProfile));
}

/** The same, outside a component (event handlers, helpers). */
export function classicNow(): boolean {
  return isClassic(useStore.getState().uiProfile);
}
