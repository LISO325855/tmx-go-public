// Settings → Safety: the dangerous-command guard's rules (Blocked, Held for me, Allowed: the whole
// safety model, there is no on/off switch) and the owner gate with its grace window (DESIGN.md
// "Safety"). Every rule change goes through the owner gate.
import { useEffect, useState } from "react";
import { AlertTriangle, Ban, ChevronRight, Fingerprint, KeyRound, LifeBuoy, Lock, LockOpen, ShieldAlert, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Skeleton } from "@/components/ui/skeleton";
import { Slider } from "@/components/ui/slider";
import { Switch } from "@/components/ui/switch";
import { CopyButton } from "@/components/copy-button";
import { PinInput, validPin } from "@/components/pin-input";
import { Section } from "@/components/page";
import { api } from "@/lib/api";
import { displayPath } from "@/lib/format";
import { clearRuleChanges, pendingChanges, savedState, stageRule, useRuleChanges } from "@/lib/rule-changes";
import { GRACE_STEPS, graceLabel, graceTick, graceUntilLabel, loadSafety, setGateStatus, useGate, useGraceUntil } from "@/lib/owner-gate";
import type { GateStatus, GuardAlwaysAllow, GuardRule, GuardSettings, PinStatus, RuleState } from "@/lib/types";
import { cn } from "@/lib/utils";

function Card({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("rounded-xl border bg-card", className)}>{children}</div>;
}

function ToggleRow({
  id,
  label,
  hint,
  checked,
  disabled,
  onChange,
}: {
  id: string;
  label: React.ReactNode;
  hint?: React.ReactNode;
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="grid grid-cols-1 items-start gap-3 px-4 py-3 md:grid-cols-[minmax(0,1fr)_auto] md:gap-4">
      <label htmlFor={id} className="min-w-0 cursor-pointer">
        <div className="text-sm">{label}</div>
        {hint && <div className="mt-0.5 text-xs text-muted-foreground">{hint}</div>}
      </label>
      <Switch id={id} checked={checked} disabled={disabled} onCheckedChange={onChange} className="order-first mt-0.5 md:order-none" />
    </div>
  );
}

/**
 * What the guard is, in place of the old on/off switch: it is always there, every agent starts,
 * and the rules below decide. Says plainly what it cannot check.
 */
function GuardCard() {
  return (
    <Card className="divide-y">
      <div className="px-4 py-3">
        <div className="flex items-center gap-1.5 text-sm font-medium">
          <ShieldCheck className="size-4 text-state-ok" />
          Command guard
          <span className="ml-1 rounded-md border px-1.5 py-px text-2xs font-medium text-muted-foreground">Always on</span>
        </div>
        <p className="mt-0.5 text-xs text-muted-foreground">
          Checks every command and file write of your agents, in every permission mode, including Bypass permissions. It has no off switch: the rules below
          are the whole safety model. Set each one to Blocked, Held for me or Allowed.
        </p>
      </div>
      <div className="flex items-start gap-2 px-4 py-2.5 text-xs text-muted-foreground">
        <AlertTriangle className="mt-px size-3.5 shrink-0 text-[color-mix(in_oklch,var(--state-waiting)_80%,var(--foreground))]" />
        <span>
          Agents always start. One the guard cannot check (a harness tmx has no guard for, or Claude settings that switch hooks off) starts too, with a red
          warning in its timeline: nothing it runs is blocked or held. What you type into a terminal is not checked either.
        </span>
      </div>
    </Card>
  );
}

const STATE_LABEL: Record<RuleState, string> = { block: "Blocked", hold: "Held for me", allow: "Allowed" };

/** Rule-log entries that are not about one rule (GuardSettings.rule_log). */
const LOG_NAME: Record<string, string> = { "master-switch": "Guard on/off switch (removed)" };

/**
 * A rule's switch (the owner, 3 Nov): three ways on the Blocked list, Held for me / Allowed on the
 * Held list. A click only chooses (lib/rule-changes.ts); "Save" above the lists sends all
 * choices together, so one PIN (or Windows Hello at the machine) covers them all (17 Nov).
 */
function RuleSwitch({ r, busy }: { r: GuardRule; busy: boolean }) {
  const staged = useRuleChanges((s) => s.staged[r.id]);
  const states: RuleState[] = r.outcome === "block" ? ["block", "hold", "allow"] : ["hold", "allow"];
  const saved = savedState(r);
  const cur = staged ?? saved;
  return (
    <div
      role="radiogroup"
      aria-label={`${r.name}: what agents get`}
      data-rule-changed={cur !== saved ? "" : undefined}
      className={cn("inline-flex shrink-0 overflow-hidden rounded-md border text-2xs", cur !== saved && "ring-2 ring-primary/40")}
      onClick={(e) => e.stopPropagation()}
    >
      {states.map((st) => (
        <button
          key={st}
          type="button"
          role="radio"
          aria-checked={cur === st}
          disabled={busy}
          onClick={() => stageRule(r, st)}
          className={cn(
            "px-2 py-1 font-medium transition-colors not-first:border-l disabled:opacity-60",
            cur === st
              ? st === "block"
                ? "bg-state-error/15 text-foreground"
                : st === "hold"
                  ? "bg-state-waiting/20 text-foreground"
                  : "bg-state-ok/15 text-foreground"
              : "text-muted-foreground hover:bg-accent/50",
          )}
        >
          {STATE_LABEL[st]}
        </button>
      ))}
    </div>
  );
}

function RuleRow({ r, busy }: { r: GuardRule; busy: boolean }) {
  const [open, setOpen] = useState(false);
  const staged = useRuleChanges((s) => s.staged[r.id]);
  const cur = staged ?? r.state ?? r.outcome;
  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <div className="grid w-full grid-cols-1 items-start gap-3 px-4 py-2.5 md:flex">
        <CollapsibleTrigger className="grid w-full min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-3 text-left outline-none hover:opacity-90 focus-visible:underline md:flex md:w-auto md:flex-1">
          {cur === "block" ? (
            <Ban className="mt-0.5 size-3.5 shrink-0 text-state-error" />
          ) : cur === "hold" ? (
            <ShieldAlert className="mt-0.5 size-3.5 shrink-0 text-[color-mix(in_oklch,var(--state-waiting)_80%,var(--foreground))]" />
          ) : (
            <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-state-ok" />
          )}
          <span className="min-w-0 md:flex-1">
            <span className="block text-[0.8125rem] font-medium">{r.name}</span>
            <span className="block text-xs text-muted-foreground">{r.summary}</span>
            {r.always_on && <span className="mt-0.5 block text-2xs text-foreground/75">Always on: {r.always_on}</span>}
          </span>
          <ChevronRight className={cn("mt-0.5 size-3.5 shrink-0 text-subtle-foreground transition-transform duration-150", open && "rotate-90")} />
        </CollapsibleTrigger>
        {r.switchable !== false && r.state && <RuleSwitch r={r} busy={busy} />}
      </div>
      <CollapsibleContent>
        <div className="space-y-2 px-4 pb-3 pl-10.5">
          <p className="text-xs leading-relaxed text-foreground/85">{r.why}</p>
          {r.examples?.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {r.examples.map((x) => (
                <code key={x} className="rounded-md border bg-code px-1.5 py-0.5 font-mono text-[0.7188rem] text-foreground/80">
                  {x}
                </code>
              ))}
            </div>
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

/** The standing "Allow always" answers: what runs without asking, for whom; Revoke asks for a proof. */
function AlwaysCard({ items }: { items: GuardAlwaysAllow[] }) {
  const [busy, setBusy] = useState("");
  async function revoke(al: GuardAlwaysAllow) {
    setBusy(al.id);
    try {
      const saved = await api.revokeAlways(al.id);
      useGate.setState((s) => (s.safety && saved ? { safety: { ...s.safety, guard: saved } } : {}));
      toast.success("Revoked; it asks you again");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy("");
    }
  }
  if (!items.length) {
    return <Card className="px-4 py-3 text-xs text-muted-foreground">None. Answer a held command with Allow always to stop being asked for exactly that command.</Card>;
  }
  return (
    <Card className="divide-y">
      {items.map((al) => (
        <div key={al.id} className="grid grid-cols-1 items-start justify-items-start gap-3 px-4 py-3 md:flex">
          <div className="min-w-0 flex-1">
            <pre className="font-mono text-[0.75rem] whitespace-pre-wrap text-foreground/90 [overflow-wrap:anywhere]">{al.command}</pre>
            <p className="mt-1 text-xs text-muted-foreground">
              {al.rule_name} · {al.tool} · {al.profile ? `agents like ${al.from || al.profile} (${al.profile})` : "any agent"}
              {al.cwd ? ` · in ${displayPath(al.cwd)}` : ""}
            </p>
          </div>
          <Button size="sm" variant="outline" disabled={busy !== ""} onClick={() => void revoke(al)} title="Needs your confirmation on this machine">
            <Fingerprint />
            Revoke
          </Button>
        </div>
      ))}
    </Card>
  );
}

function RulesCard({ rules, log }: { rules: GuardRule[]; log?: GuardSettings["rule_log"] }) {
  const onSaved = (g: GuardSettings, next: GuardRule[]) =>
    useGate.setState((s) => (s.safety ? { safety: { ...s.safety, guard: g, rules: next } } : {}));
  const staged = useRuleChanges((s) => s.staged);
  const pending = pendingChanges(staged, rules);
  const [busy, setBusy] = useState(false);
  async function saveAll() {
    if (busy || pending.length === 0) return;
    setBusy(true);
    try {
      const out = await api.putRuleStates(Object.fromEntries(pending.map((p) => [p.rule.id, p.next])));
      if (out) onSaved(out.guard, out.rules);
      clearRuleChanges();
      toast.success(pending.length === 1 ? `${pending[0].rule.name}: ${STATE_LABEL[pending[0].next]}` : `${pending.length} rules changed`);
    } catch (e) {
      toast.error((e as Error).message); // the choices stay, to try again
    } finally {
      setBusy(false);
    }
  }
  // The fixed rules have no switch: they are all Blocks and get their own read-only list.
  const shown = rules.filter((r) => r.switchable !== false);
  const never = rules.filter((r) => r.switchable === false);
  // One list per state, by what each rule does now: a rule moves when you flip it.
  const now = (r: GuardRule): RuleState => r.state ?? r.default ?? r.outcome;
  const groups: { title: string; hint: string; items: GuardRule[] }[] = [
    { title: "Blocked", hint: "Never runs; the agent is told why. Choose, then Save: one PIN covers all your changes.", items: shown.filter((r) => now(r) === "block") },
    { title: "Held for me", hint: "Waits in Needs you (Allow once, Allow always or Deny; no answer in 10 minutes means no).", items: shown.filter((r) => now(r) === "hold") },
    { title: "Allowed", hint: "Runs without asking. The parts marked Always on stay blocked whatever you choose.", items: shown.filter((r) => now(r) === "allow") },
    ...(never.length ? [{ title: "Always blocked", hint: "No switch: these never run, whatever the lists above say.", items: never }] : []),
  ];
  return (
    <div className="space-y-3">
      {pending.length > 0 && (
        <Card className="sticky top-2 z-10 border-primary/40 px-4 py-2.5">
          <div className="flex flex-wrap items-center gap-2">
            <p className="min-w-0 flex-1 text-[0.8125rem]">
              <span className="font-semibold">{pending.length === 1 ? "1 change not saved yet" : `${pending.length} changes not saved yet`}</span>
              <span className="block text-xs text-muted-foreground">{pending.map((p) => `${p.rule.name}: ${STATE_LABEL[p.next]}`).join(" · ")}</span>
            </p>
            <Button size="sm" onClick={() => void saveAll()} disabled={busy}>
              <Fingerprint />
              {pending.length === 1 ? "Save" : `Save all ${pending.length}`}
            </Button>
            <Button size="sm" variant="ghost" onClick={clearRuleChanges} disabled={busy}>
              Discard
            </Button>
          </div>
        </Card>
      )}
      {groups.map((gr) => (
        <Card key={gr.title} className="overflow-hidden">
          <div className="grid grid-cols-1 gap-2 border-b px-4 py-2.5 md:flex md:items-baseline">
            <h3 className="text-[0.8125rem] font-semibold">{gr.title}</h3>
            <p className="text-xs text-muted-foreground">{gr.hint}</p>
          </div>
          <div className="divide-y">
            {gr.items.map((r) => (
              <RuleRow key={r.id} r={r} busy={busy} />
            ))}
            {gr.items.length === 0 && <p className="px-4 py-2.5 text-xs text-muted-foreground">No rule is set to this.</p>}
          </div>
        </Card>
      ))}
      <p className="text-xs text-muted-foreground">Everything else runs as usual; the agent's permission mode still applies.</p>
      {log && log.length > 0 && (
        <Card className="overflow-hidden">
          <div className="border-b px-4 py-2.5 text-[0.8125rem] font-semibold">Changes</div>
          <ul className="max-h-56 divide-y overflow-auto text-xs">
            {[...log].reverse().slice(0, 50).map((ch, i) => (
              <li key={i} className="grid min-w-0 grid-cols-1 gap-2 px-4 py-1.5 md:flex md:items-baseline">
                <span className="min-w-0 w-full whitespace-normal break-words md:w-auto md:flex-1 md:truncate">{rules.find((r) => r.id === ch.rule)?.name ?? LOG_NAME[ch.rule] ?? ch.rule}</span>
                <span className="text-muted-foreground">
                  {STATE_LABEL[ch.old as RuleState] ?? ch.old} → <span className="text-foreground">{STATE_LABEL[ch.new as RuleState] ?? ch.new}</span>
                </span>
                <span className="tabular min-w-0 w-full text-2xs text-subtle-foreground md:w-36 md:shrink-0 md:text-right">
                  {ch.by === "tmx" ? "tmx defaults · " : ""}
                  {new Date(ch.at).toLocaleString([], { dateStyle: "short", timeStyle: "short" })}
                </span>
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  );
}

/**
 * The grace window: after a proof the daemon stops asking for this long (a dial of fixed steps).
 * Longer asks for a fresh proof (the sheet then names the new length); shorter, Off and Lock now
 * never do. The window lives in the daemon's memory, so a restart locks it.
 */
function GraceRow({ gate }: { gate: GateStatus }) {
  const steps = gate.grace_steps?.length ? gate.grace_steps : GRACE_STEPS;
  const minutes = gate.grace_minutes ?? 0;
  const until = useGraceUntil();
  const [draft, setDraft] = useState<number | null>(null); // the step being dragged or saved
  const [busy, setBusy] = useState(false);
  const at = steps.indexOf(minutes);
  const idx = draft ?? (at >= 0 ? at : 0);
  const shown = steps[idx] ?? 0;
  const disabled = busy || !gate.available;

  async function save(i: number) {
    const next = steps[i];
    if (next === undefined || next === minutes) {
      setDraft(null);
      return;
    }
    setDraft(i);
    setBusy(true);
    if (next > minutes) useGate.setState({ graceAsked: next });
    try {
      const st = await api.putGrace(next);
      if (st) setGateStatus(st);
      toast.success(next === 0 ? "tmx asks every time" : `No prompt for ${graceLabel(next)} after a confirmation`);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
      setDraft(null);
      useGate.setState({ graceAsked: null });
    }
  }

  async function lock() {
    setBusy(true);
    try {
      setGateStatus(await api.lockGate());
      toast.success("Locked: the next action asks again");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="px-4 py-3" data-testid="grace">
      <div className="flex items-baseline gap-2">
        <div className="text-sm">Don't ask again for</div>
        <div className="ml-auto text-sm font-medium tabular-nums">{graceLabel(shown)}</div>
      </div>
      <p className="mt-0.5 text-xs text-muted-foreground">
        After you confirm once, tmx skips the prompt for this long. You still decide each action.
      </p>
      <div className="mt-3.5">
        <Slider
          min={0}
          max={steps.length - 1}
          step={1}
          value={[idx]}
          disabled={disabled}
          onValueChange={([i]) => setDraft(i)}
          onValueCommit={([i]) => void save(i)}
          thumbLabel="Don't ask again for"
          valueText={graceLabel(shown)}
        />
        <div className="mt-1.5 flex h-4 justify-between px-2">
          {steps.map((m, i) => (
            <span key={m} className="relative w-0">
              <button
                type="button"
                tabIndex={-1}
                disabled={disabled}
                onClick={() => void save(i)}
                className={cn(
                  "absolute left-1/2 -translate-x-1/2 px-0.5 text-2xs whitespace-nowrap text-muted-foreground tabular-nums outline-none hover:text-foreground disabled:pointer-events-none",
                  i === idx && "font-medium text-foreground",
                )}
              >
                {graceTick(m)}
              </button>
            </span>
          ))}
        </div>
      </div>
      <div className="mt-3 flex min-h-9 flex-wrap items-center gap-x-2 gap-y-1.5 rounded-lg border bg-muted/40 px-3 py-1.5 text-xs">
        {until ? (
          <>
            <LockOpen className="size-3.5 shrink-0 text-brand" />
            <span>
              No confirmation needed until <span className="font-medium tabular-nums">{graceUntilLabel(until)}</span>
            </span>
            <Button size="xs" variant="outline" className="ml-auto" disabled={busy} onClick={() => void lock()}>
              <Lock />
              Lock now
            </Button>
          </>
        ) : (
          <>
            <Lock className="size-3.5 shrink-0 text-subtle-foreground" />
            <span className="text-muted-foreground">{minutes === 0 ? "Asks every time." : "Locked: the next action asks."}</span>
          </>
        )}
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        {gate.available ? "" : "Needs a way to confirm on this machine. "}
        Kept in memory only: a restart or update locks it. Only this UI session can use its window. Turning the gate on or off, and a
        longer window, always ask.
      </p>
    </div>
  );
}

/**
 * Windows Hello / Touch ID at this machine: an optional shortcut beside the owner PIN, which
 * works on every device (#187). Off is refused while no PIN is set: it would be the last way in.
 */
function HelloRow({ gate }: { gate: GateStatus }) {
  const [busy, setBusy] = useState(false);
  const on = !!gate.hello_shortcut;
  async function toggle(next: boolean) {
    setBusy(true);
    try {
      const st = await api.putHello(next);
      if (st) setGateStatus(st);
      toast.success(next ? "Windows Hello is offered too" : "Only your owner PIN now");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <ToggleRow
      id="gate-hello"
      label="Also accept Windows Hello on this machine"
      hint={
        gate.pin_set
          ? "Your owner PIN always works, here and on your paired devices. This only adds a shortcut at this machine."
          : "Set your owner PIN first: until then Windows Hello is the only way to confirm here."
      }
      checked={on}
      disabled={busy || (on && !gate.pin_set)}
      onChange={(v) => void toggle(v)}
    />
  );
}

function GateCard() {
  const gate = useGate((s) => s.safety?.gate);
  const [busy, setBusy] = useState(false);
  if (!gate) return null;
  const remote = gate.method === "pin";

  async function toggle(on: boolean) {
    setBusy(true);
    try {
      const st = await api.putGate(on);
      if (st) setGateStatus(st);
      toast.success(on ? "Owner gate on" : "Owner gate off");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="divide-y">
      <div className="grid grid-cols-1 items-start justify-items-start gap-3 px-4 py-3 md:flex">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5 text-sm font-medium">
            <Fingerprint className={cn("size-4", gate.enabled ? "text-brand" : "text-subtle-foreground")} />
            Owner gate
            <span
              className={cn(
                "ml-1 rounded-md border px-1.5 py-px text-2xs font-medium",
                gate.enabled ? "border-brand/30 bg-brand/10 text-foreground" : "text-muted-foreground",
              )}
            >
              {gate.enabled ? "On" : "Off"}
            </span>
          </div>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {remote ? "Owner actions ask for your owner PIN on this device. Change the gate switch and grace settings on the host computer." : gate.available
              ? `The daemon asks for ${gate.method_label} on this machine before the actions below. Once enrolled, missing state keeps it on; turning it on or off asks once too.`
              : `Not available here: ${gate.unavailable || "no way to ask the owner on this machine"}.`}
          </p>
        </div>
        <Button
          size="sm"
          variant={gate.enabled ? "outline" : "default"}
          disabled={remote || busy || (!gate.enabled && !gate.available)}
          onClick={() => void toggle(!gate.enabled)}
          className="shrink-0"
        >
          <Fingerprint />
          {gate.enabled ? "Turn off" : "Turn on"}
        </Button>
      </div>
      {!remote && gate.grace_steps && <GraceRow gate={gate} />}
      {!remote && gate.pin_set !== undefined && <HelloRow gate={gate} />}
      <div className="px-4 py-3">
        <div className="mb-1.5 text-xs font-medium text-muted-foreground">{gate.enabled ? "Asks before" : "Will ask before"}</div>
        <ul className="grid gap-1 text-[0.8125rem] md:grid-cols-2">
          {gate.gated.map((x) => (
            <li key={x} className="flex items-start gap-1.5">
              <span className="mt-[0.4375rem] size-1 shrink-0 rounded-full bg-subtle-foreground" />
              {x}
            </li>
          ))}
        </ul>
      </div>
      <div className="flex items-start gap-2.5 px-4 py-3 text-xs text-muted-foreground">
        <LifeBuoy className="mt-px size-3.5 shrink-0" />
        <div className="min-w-0 space-y-1">
          <p>
            {gate.recovery || "The gate protects tmx actions, never your computer login or files. See the documented offline recovery procedure if confirmation stops working."}
          </p>
          {gate.file && (
            <div className="flex min-w-0 items-center gap-1">
              <code className="truncate font-mono text-[0.7188rem] text-foreground/85" title={gate.file}>
                {displayPath(gate.file)}
              </code>
              <CopyButton text={gate.file} label="Copy path" />
            </div>
          )}
        </div>
      </div>
    </Card>
  );
}

// The owner PIN: ONE short code (6 to 12 digits) for every owner step from a phone or another
// browser, the same on every machine and in the Python tmx. Set it here at the machine; from
// a browser it changes only with the current one. Wrong tries slow down, never lock out.
function PinCard() {
  const [st, setSt] = useState<PinStatus | null>(null);
  const [pin, setPin] = useState("");
  const [again, setAgain] = useState("");
  const [old, setOld] = useState("");
  const [busy, setBusy] = useState(false);
  const load = () => api.pinStatus().then(setSt, () => setSt(null));
  useEffect(() => {
    void load();
  }, []);
  if (!st?.available) return null;
  const here = !!st.at_machine;
  const needOld = st.set && !here;
  const ok = validPin(pin) && pin === again && (!needOld || validPin(old));

  async function save() {
    setBusy(true);
    try {
      await api.setPin(pin, needOld ? old : undefined);
      toast.success(st?.set ? "Owner PIN changed on this machine (the others pick it up within 5 min)" : "Owner PIN set");
      setPin("");
      setAgain("");
      setOld("");
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function clearWaits() {
    try {
      await api.clearPinWaits();
      toast.success("Wrong-PIN waits cleared");
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    }
  }

  return (
    <Card className="divide-y">
      <div className="px-4 py-3">
        <div className="flex items-center gap-1.5 text-sm font-medium">
          <KeyRound className={cn("size-4", st.set ? "text-brand" : "text-subtle-foreground")} />
          Owner PIN
          <span className="ml-1 rounded-md border px-1.5 py-px text-2xs font-medium text-muted-foreground">{st.set ? "Set" : "Not set"}</span>
        </div>
        <p className="mt-0.5 text-xs text-muted-foreground">
          One short code for approvals and pairing from your phone or another browser: the same on all your machines. Wrong tries make you wait a little, never lock you out.
          {!here && !st.set && " Set it on the machine itself."}
          {!here && st.set && " Lost it? Set a new one on the machine itself."}
        </p>
        {st.error && <p className="mt-1 text-xs text-destructive">{st.error}</p>}
        {(here || st.set) && (
          <form
            className="mt-3 grid max-w-xs gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (ok) void save();
            }}
          >
            {needOld && <PinInput label="Current PIN" value={old} onChange={setOld} />}
            <PinInput label={st.set ? "New PIN" : "PIN"} value={pin} onChange={setPin} />
            <PinInput label="The same PIN again" value={again} onChange={setAgain} />
            {again && pin !== again && <p className="text-xs text-destructive">The two PINs differ.</p>}
            <div className="flex gap-2">
              <Button type="submit" size="sm" disabled={!ok || busy}>
                {st.set ? "Change PIN" : "Set PIN"}
              </Button>
              {here && st.set && (
                <Button type="button" size="sm" variant="outline" onClick={() => void clearWaits()}>
                  Clear wrong-PIN waits
                </Button>
              )}
            </div>
          </form>
        )}
      </div>
    </Card>
  );
}

export function SafetySection() {
  const safety = useGate((s) => s.safety);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    void loadSafety().then((s) => live && setFailed(!s));
    return () => {
      live = false;
    };
  }, []);

  return (
    <Section title="Safety">
      <p className="-mt-1 mb-3 text-xs text-muted-foreground">What agents may never do, what waits for you, and how tmx makes sure it is you.</p>
      {!safety ? (
        failed ? (
          <Card className="px-4 py-3 text-sm text-muted-foreground">This daemon doesn't report its safety settings. Update tmx.</Card>
        ) : (
          <div className="space-y-3">
            <Skeleton className="h-40 rounded-xl" />
            <Skeleton className="h-24 rounded-xl" />
          </div>
        )
      ) : (
        <div className="space-y-6">
          <GuardCard />
          <div>
            <h3 className="mb-2 text-xs font-medium text-muted-foreground">Allowed always</h3>
            <AlwaysCard items={safety.guard.always_allow ?? []} />
          </div>
          <div>
            <h3 className="mb-2 text-xs font-medium text-muted-foreground">Rules</h3>
            <RulesCard rules={safety.rules} log={safety.guard.rule_log} />
          </div>
          <div>
            <h3 className="mb-2 text-xs font-medium text-muted-foreground">Confirmation</h3>
            <GateCard />
          </div>
          <div>
            <h3 className="mb-2 text-xs font-medium text-muted-foreground">Owner PIN</h3>
            <PinCard />
          </div>
        </div>
      )}
    </Section>
  );
}
