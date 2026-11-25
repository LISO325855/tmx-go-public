// Device-local shortcuts, scoped by daemon machine and stable agent ID.
const KEY = "tmx.agentPins";
export function agentPinKey(machine: string, id: string): string { return JSON.stringify([machine, id]); }
export function savedAgentPins(): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(KEY) ?? "[]");
    return Array.isArray(value) ? [...new Set(value.filter((x): x is string => typeof x === "string"))] : [];
  } catch { return []; }
}
export function saveAgentPins(pins: string[]): void {
  try { localStorage.setItem(KEY, JSON.stringify(pins)); } catch { /* Keep the choice for this session in private mode. */ }
}
export function toggleAgentPin(pins: string[], key: string): string[] {
  return pins.includes(key) ? pins.filter((p) => p !== key) : [...pins, key];
}
