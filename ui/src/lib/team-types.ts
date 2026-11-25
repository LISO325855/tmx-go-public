// Team status (DESIGN.md "Team status"): GET /api/team-status, the same JSON names as Go's
// internal/team and internal/chat/team.go.

export type Tone = "ok" | "warn" | "bad" | "none";

export interface TeamReport {
  report: number;
  contract: number;
  sent_at: number;
  node: { label: string; os: string; build: string; based_on: string; personal: boolean; update: "pinned" | "auto" | "off" | "waiting" | string; started_at: number };
  hours?: { days?: string[]; from?: string; to?: string; tz?: string };
  /** agents and agents_working: counts, sent since the fleet build (absent from an older tmx) */
  fleet: { agents_running: number; waiting_for_owner: number; ai_accounts: Record<string, string> | null; agents?: number; agents_working?: number };
  links: { member: string; state: "direct" | "relayed" | "offline" | string }[] | null;
  phone: { state: "connected" | "not_set" | string; paired: number; last_seen_at?: number; notifications: boolean; owner_gate: string };
  whatsapp: { state: "connected" | "not_set" | "error" | string; authorised_chats?: number; last_event_at?: number };
  email: { state: "connected" | "not_set" | "error" | string; via?: "mcp" | "companion" | string };
  health: { disk_ok: boolean; memory_ok: boolean; last_error?: string };
  /** Whether the owner's assistant is linked on that computer (since the fleet build). */
  companion?: { state: "connected" | "not_set" | "error" | string };
  /** Today on that computer, as two counts and nothing about the work itself (since the fleet build). */
  activity?: { day: string; active_minutes: number; agent_minutes: number };
}

export interface TeamCheck {
  id: string;
  title: string;
  result: "pass" | "fail" | "not_checked";
  detail?: string;
}

export interface TeamChange {
  at: number;
  field: string;
  old: string;
  new: string;
}

export interface TeamNodeRow {
  peer_id: string;
  me?: boolean;
  admin?: boolean;
  has_report: boolean;
  label: string;
  build_state: "current" | "old" | "below_floor" | "unknown" | "fleet";
  seen: "now" | "today" | "offline" | "never";
  reported_at?: number;
  links: { direct: number; relayed: number; offline: number; members: number };
  linked: boolean;
  channels: number;
  report?: TeamReport;
  checks: TeamCheck[];
  passed: number;
  checked: number;
  changes?: TeamChange[] | null;
}

export interface TeamPerson {
  name: string;
  me?: boolean;
  admin?: boolean;
  nodes: TeamNodeRow[];
  passing: boolean;
  linked: boolean;
  channels: boolean;
}

export interface TeamSummary {
  people: number;
  passing: number;
  builds: string[] | null;
  linked: number;
  channels: number;
  newest?: string;
}

export interface TeamGroup {
  chat_id: string;
  name: string;
  admin_name: string;
  admin: boolean;
  open: boolean;
  /** all: every member's rows; own: only this computer's */
  scope: "all" | "own";
  reports: number;
  people: TeamPerson[];
  summary: TeamSummary;
}

export interface TeamSharing {
  chat_id: string;
  name: string;
  admin_id: string;
  admin_name: string;
  admin: boolean;
  collects: boolean;
  share: "unasked" | "on" | "off";
  open: boolean;
}

export interface TeamPage {
  visible: boolean;
  groups: TeamGroup[];
  sharing: TeamSharing[];
  self?: TeamNodeRow;
  contract: number;
  floor: string;
  checks: number;
}
