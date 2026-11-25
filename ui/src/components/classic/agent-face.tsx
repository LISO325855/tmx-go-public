// An agent's little head (classic UI profile; lib/agent-face.ts): Python's pixel face on its
// coloured tile. size is the tile's edge in px (Python: 34 in the list, 24 on a project card).
import { agentFaceParts } from "@/lib/agent-face";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

export function AgentFace({ name, size = 24, title, className }: { name: string; size?: number; title?: string; className?: string }) {
  const machine = useStore((s) => s.machine);
  const f = agentFaceParts(name, machine);
  const face = Math.round(size * 0.82);
  return (
    <span
      role="img"
      aria-label={name}
      title={title ?? name}
      data-agent-face={name}
      className={cn("inline-grid shrink-0 place-items-center", className)}
      style={{
        width: size,
        height: size,
        borderRadius: Math.round(size * 0.3),
        background: `linear-gradient(145deg,#ffffff18,transparent 65%),hsl(${f.hue} 64% ${f.tile}%)`,
      }}
    >
      <svg viewBox="0 0 24 24" width={face} height={face} aria-hidden="true" focusable="false" shapeRendering="crispEdges" style={{ display: "block", filter: "drop-shadow(0 1px 0 #0003)" }}>
        <g fill="#fffbea">
          <path d={f.crown} />
          <path d={f.head} />
          <path opacity=".65" d={f.ears} />
        </g>
        <g fill="#202026">
          <path d={f.eyes} />
          <path d={f.mouth} />
        </g>
        <path fill={`hsl(${f.hue} 70% 55%)`} opacity=".5" d={f.cheeks} />
      </svg>
    </span>
  );
}
