// Ephemeral UI state: dialogs, the mobile nav sheet, focus requests.
import { create } from "zustand";
import { savedPanes, savePanes, type PaneLayout } from "./agent-view";
import { savedAgentPins, saveAgentPins, toggleAgentPin } from "./agent-pins";
import { savedShowStopped } from "./classic-agents";

interface UIState {
  agentPins: string[];
	showAllAgents: boolean;
  /** classic: stopped agents are in the list too (remembered on the device: lib/classic-agents.ts) */
  showStoppedAgents: boolean;
  /** classic, wide screen: Chat and Terminal side by side, and which panes show (lib/agent-view.ts) */
  agentPanes: PaneLayout;
  /** the New agent dialog, opened from anywhere; projectId: the project it starts on */
  newAgent: false | { projectId?: string };
  palette: boolean;
  shortcuts: boolean;
  newProject: boolean;
  /** the first-run setup (components/onboarding.tsx) */
  onboarding: boolean;
  /** the "Connect a phone" dialog */
  connectPhone: boolean;
  mobileNav: boolean;
  /** bumped to ask the current page to focus its composer */
  focusComposer: number;
  /** bumped to ask the overview to focus the head composer */
  focusHead: number;
}

export const useUI = create<UIState>(() => ({
  agentPins: savedAgentPins(),
	showAllAgents: false,
  showStoppedAgents: savedShowStopped(),
  agentPanes: savedPanes(),
  newAgent: false,
  palette: false,
  shortcuts: false,
  newProject: false,
  onboarding: false,
  connectPhone: false,
  mobileNav: false,
  focusComposer: 0,
  focusHead: 0,
}));

export const ui = {
  toggleAgentPin: (key: string) => useUI.setState((s) => {
    const agentPins = toggleAgentPin(s.agentPins, key);
    saveAgentPins(agentPins);
    return { agentPins };
  }),
  set: useUI.setState,
  openPalette: (v = true) => useUI.setState({ palette: v }),
  openShortcuts: (v = true) => useUI.setState({ shortcuts: v }),
  openNewProject: (v = true) => useUI.setState({ newProject: v, palette: false, mobileNav: false }),
  /** the pane layout of the agent view: one choice for all agents, remembered on the device */
  setAgentPanes: (l: PaneLayout) => {
    savePanes(l);
    useUI.setState({ agentPanes: l });
  },
  /** "+ Agent" from anywhere (sidebar, Ctrl K): the form asks for the project; projectId preselects one. */
  openNewAgent: (v = true, projectId?: string) => useUI.setState({ newAgent: v ? { projectId } : false, palette: false, mobileNav: false }),
  openMobileNav: (v = true) => useUI.setState({ mobileNav: v }),
  openOnboarding: (v = true) => useUI.setState({ onboarding: v, palette: false, mobileNav: false }),
  /** "Connect a phone" (components/connect-phone.tsx): at the computer only */
  openConnectPhone: (v = true) => useUI.setState({ connectPhone: v, palette: false, mobileNav: false }),
  focusComposer: () => useUI.setState((s) => ({ focusComposer: s.focusComposer + 1 })),
  focusHead: () => useUI.setState((s) => ({ focusHead: s.focusHead + 1 })),
};

export function isMacLike(): boolean {
  return /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent);
}

export const MOD = isMacLike() ? "⌘" : "Ctrl";
