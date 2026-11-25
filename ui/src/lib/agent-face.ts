// The little agent heads of the Python dashboard (the owner, 17 Nov 19:01: "all the little agent
// heads… if we can get that back"). A pixel face on a coloured tile, the same for an agent
// wherever it shows, drawn from its name and machine. Ported one-to-one from tmx Python's
// static/index.html (agentVisualSeed, workspaceAgentHue, agentFace, avatar), so an agent that
// kept its name and machine has the face the owner knows. Pure; the component is
// components/classic/agent-face.tsx.

/** Python's agentVisualSeed: FNV-1a over "name@host", as an unsigned 32-bit number. */
export function agentSeed(name: string, host: string): number {
  let n = 2166136261;
  for (const c of `${name}@${host}`) n = Math.imul(n ^ c.charCodeAt(0), 16777619);
  return n >>> 0;
}

const HUES = [218, 272, 326, 8, 32, 154, 180, 200];

/** Python's workspaceAgentHue: one of eight tile colours. */
export function agentHue(seed: number): number {
  return HUES[seed % 8];
}

/** The tile's lightness: the yellow-green hues are drawn darker so the cream face stays readable. */
export function tileLightness(hue: number): number {
  return hue > 25 && hue < 195 ? 29 : 40;
}

const CROWNS = ["M11 3h2v4h-2zM10 2h4v2h-4z", "M6 4h3v4H6zM15 4h3v4h-3z", "M8 4h8v3H8zM10 2h4v2h-4z", "M5 5h3v3H5zM16 3h3v5h-3z"];
const EYES = ["M7 11h3v3H7zM14 11h3v3h-3z", "M7 11h3v2H7zM14 11h3v2h-3zM8 10h2v1H8zM14 10h2v1h-2z", "M7 10h3v4H7zM14 10h3v4h-3z", "M7 11h3v3H7zM14 12h3v2h-3z"];
const MOUTHS = ["M9 16h2v1h2v-1h2v2H9z", "M10 16h4v2h-4z", "M9 16h6v1H9z"];

export interface AgentFaceParts {
  hue: number;
  /** lightness of the tile, in percent */
  tile: number;
  crown: string;
  /** the head's outline (a wide or a narrow head) */
  head: string;
  /** the two ears */
  ears: string;
  eyes: string;
  mouth: string;
  /** the cheeks, in the tile's hue */
  cheeks: string;
}

/** Python's agentFace: the SVG paths of the face in a 24 x 24 box. */
export function agentFaceParts(name: string, host: string): AgentFaceParts {
  const n = agentSeed(name, host);
  const wide = (n >>> 4) % 2;
  const x = wide ? 4 : 5;
  const w = wide ? 16 : 14;
  const hue = agentHue(n);
  return {
    hue,
    tile: tileLightness(hue),
    crown: CROWNS[(n >>> 6) % 4],
    head: `M${x + 2} 6h${w - 4}v1h2v2h2v10h-2v2H${x + 2}v-2H${x}V9h2z`,
    ears: "M2 11h2v6H2zM20 11h2v6h-2z",
    eyes: EYES[(n >>> 8) % 4],
    mouth: MOUTHS[(n >>> 10) % 3],
    cheeks: "M6 15h2v1H6zM16 15h2v1h-2z",
  };
}
