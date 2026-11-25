import { useMemo } from "react";
import { agentVisible } from "@/lib/active-agents";
import { classicAgentVisible } from "@/lib/classic-agents";
import { useStore } from "@/lib/store";
import { useUI } from "@/lib/ui";
import { useClassic } from "./use-classic";
import { useNow } from "./use-now";

export function useVisibleAgents() {
  const agents = useStore((s) => s.agents);
  const showAll = useUI((s) => s.showAllAgents);
  const showStopped = useUI((s) => s.showStoppedAgents);
  const classic = useClassic();
  const now = useNow(30_000);
  // Classic (architect, 17 Nov 15:05): Python's rule, every agent except shelved and stopped
  // ones; the default dashboard keeps its 2-hour rule and "Show all".
  return useMemo(
    () => Object.fromEntries(Object.entries(agents).filter(([, a]) => (classic ? classicAgentVisible(a, showStopped) : agentVisible(a, showAll, now)))),
    [agents, showAll, showStopped, classic, now],
  );
}
