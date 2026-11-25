// The first-run setup (lib/onboarding.ts; DESIGN.md "Onboarding"), in the manner of a phone's setup
// assistant: one thing per screen, a big title, a sentence or two, one button that does it, a
// quiet way to skip. Welcome → WhatsApp → a phone → Companion → Done. Every step sees by itself
// when it is done (events, or a status it reads again every few seconds) and moves on; a step
// that is already done says so with Continue. It drives the flows that exist: WhatsApp's link
// (lib/whatsapp.ts), Connect with Companion (POST /api/companion/open-connect), and for the phone
// this line's own pairing (docs/PHONE-PAIRING.md): the phone opens the computer's HTTPS address
// and pairs itself with the owner PIN, or the owner approves here the code it shows. Every skipped
// step is still there afterwards: Settings → Setup opens this again.
//
// The screens, their look and the WhatsApp and Companion steps are Mustneer's (tmx-go 2996c97,
// PR #342); the phone step is rewritten for this line, which has no one-scan phone connect.
import { useCallback, useEffect, useRef, useState } from "react";
import { Check, CheckCircle2, ChevronLeft, Loader2, MessageCircle, Smartphone, Sparkles, TriangleAlert, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { QRCode } from "@/components/chat/qr-code";
import { api } from "@/lib/api";
import { companionApi, useCompanion } from "@/lib/companion";
import { companionHalves, markOnboarded, ONBOARD_STEPS, pairedDevices, pairingCode, phoneAddress, phoneWay, type OnboardStep } from "@/lib/onboarding";
import { onBusEvent, useStore } from "@/lib/store";
import type { ConfigView, PinStatus } from "@/lib/types";
import { ui, useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { isLinked, loadWhatsApp, useWa, waApi } from "@/lib/whatsapp";

/** How often the Companion step reads Companion's state again. */
const COMPANION_EVERY = 2500;
/** How often the phone step looks for a newly paired phone while it shows the code. */
const PHONE_EVERY = 3000;
/** How long a step shows its check before moving on by itself. */
const ADVANCE_AFTER = 1300;

export function OnboardingDialog() {
  const open = useUI((s) => s.onboarding);
  const close = () => {
    ui.openOnboarding(false);
    void markOnboarded();
  };
  return (
    <Dialog open={open} onOpenChange={(v) => !v && close()}>
      <DialogContent
        showCloseButton={false}
        className={cn(
          "flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-y-auto rounded-3xl p-0 sm:max-w-[30rem]",
          // a full-screen sheet on a narrow window
          "max-sm:top-0 max-sm:left-0 max-sm:h-dvh max-sm:max-h-dvh max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-none max-sm:border-0",
        )}
      >
        {open && <Onboarding onClose={close} />}
      </DialogContent>
    </Dialog>
  );
}

/** The screens inside the dialog. start: the step it opens on (the render check shows each one). */
export function Onboarding({ onClose, start = "welcome" }: { onClose: () => void; start?: OnboardStep }) {
  const [index, setIndex] = useState(() => ONBOARD_STEPS.indexOf(start));
  const [dir, setDir] = useState<1 | -1>(1);
  const [phones, setPhones] = useState<number | null>(null); // how many phones or other devices are paired; null = not read yet
  const step = ONBOARD_STEPS[index];

  const wa = useWa((s) => s.status);
  const companionStatus = useCompanion((s) => s.status);

  const go = useCallback(
    (to: OnboardStep) => {
      const n = ONBOARD_STEPS.indexOf(to);
      setDir(n >= index ? 1 : -1);
      setIndex(n);
    },
    [index],
  );
  const next = useCallback(() => {
    setDir(1);
    setIndex((i) => Math.min(i + 1, ONBOARD_STEPS.length - 1));
  }, []);

  // Where things stand, for the steps and the summary: WhatsApp, paired devices, Companion.
  const readPhones = useCallback(
    () =>
      void api.devices().then(
        (list) => setPhones(pairedDevices(list)),
        () => setPhones(null),
      ),
    [],
  );
  useEffect(() => {
    void loadWhatsApp();
    void companionApi.status(false).catch(() => {});
    readPhones();
    return onBusEvent((ev) => {
      if (ev.type === "device.paired" || ev.type === "device.revoked") readPhones();
    });
  }, [readPhones]);

  let body: React.ReactNode;
  switch (step) {
    case "welcome":
      body = <Welcome onStart={next} onLater={onClose} />;
      break;
    case "whatsapp":
      body = <WhatsAppStep onNext={next} />;
      break;
    case "phone":
      body = <PhoneStep paired={phones} onRead={readPhones} onNext={next} />;
      break;
    case "companion":
      body = <CompanionStep onNext={next} />;
      break;
    default:
      body = <Done whatsapp={wa && isLinked(wa)} phone={phones === null ? null : phones > 0} companion={companionStatus && companionHalves(companionStatus).done} onGo={go} onFinish={onClose} />;
  }

  return (
    <div className="flex min-h-full flex-1 flex-col px-6 pt-4 pb-6 sm:px-9 sm:pb-8">
      <div className="flex h-9 items-center justify-between">
        {index > 0 && step !== "done" ? (
          <Button variant="ghost" size="sm" className="-ml-2 text-muted-foreground" onClick={() => go(ONBOARD_STEPS[index - 1])}>
            <ChevronLeft />
            Back
          </Button>
        ) : (
          <span />
        )}
        <Button variant="ghost" size="icon-sm" className="-mr-2 text-muted-foreground" aria-label="Close setup" onClick={onClose}>
          <X />
        </Button>
      </div>
      <div
        key={step}
        className={cn(
          "flex flex-1 flex-col animate-in fade-in-0 duration-300 ease-out motion-reduce:animate-none",
          dir > 0 ? "slide-in-from-right-6" : "slide-in-from-left-6",
        )}
      >
        {body}
      </div>
      <Dots index={index} />
    </div>
  );
}

function Dots({ index }: { index: number }) {
  return (
    <div className="mt-6 flex justify-center gap-2" role="img" aria-label={`Step ${index + 1} of ${ONBOARD_STEPS.length}`}>
      {ONBOARD_STEPS.map((s, i) => (
        <span
          key={s}
          className={cn("h-2 rounded-full transition-all duration-300 motion-reduce:transition-none", i === index ? "w-5 bg-foreground" : "w-2 bg-foreground/20")}
        />
      ))}
    </div>
  );
}

/** One screen: the picture, the title, the words, what the step shows, its one button and the quiet one. */
function Screen({
  art,
  title,
  text,
  children,
  primary,
  secondary,
  compact,
}: {
  art: React.ReactNode;
  title: string;
  text?: React.ReactNode;
  children?: React.ReactNode;
  primary?: React.ReactNode;
  secondary?: React.ReactNode;
  /** a screen with a code to scan: a smaller picture and less air, so it fits a laptop screen */
  compact?: boolean;
}) {
  return (
    <div className="flex flex-1 flex-col items-center text-center">
      <div className={cn("mt-2", compact && "mt-0 [&>div]:size-14 [&>div]:rounded-2xl [&_svg]:size-7")}>{art}</div>
      <DialogTitle className={cn("text-[1.625rem] leading-tight font-semibold tracking-tight text-balance", compact ? "mt-3 text-[1.375rem]" : "mt-5")}>{title}</DialogTitle>
      {text ? (
        <DialogDescription className="mt-2 max-w-[22rem] text-[0.9375rem] leading-relaxed text-pretty text-muted-foreground">{text}</DialogDescription>
      ) : (
        <DialogDescription className="sr-only">{title}</DialogDescription>
      )}
      {children && <div className={cn("flex w-full flex-col items-center", compact ? "mt-4" : "mt-6")}>{children}</div>}
      <div className={cn("mt-auto flex w-full flex-col items-center gap-1", compact ? "pt-5" : "pt-8")}>
        {primary}
        {secondary}
      </div>
    </div>
  );
}

/** The rounded picture at the top of a screen. */
function Art({ icon: Icon, tone = "brand", spin }: { icon: React.ComponentType<{ className?: string }>; tone?: "brand" | "ok" | "wa" | "error"; spin?: boolean }) {
  return (
    <div
      className={cn(
        "flex size-[4.5rem] items-center justify-center rounded-[1.375rem] shadow-sm",
        tone === "brand" && "bg-brand text-brand-foreground",
        tone === "ok" && "bg-state-ok text-white",
        tone === "wa" && "bg-[#25D366] text-white",
        tone === "error" && "bg-state-error/12 text-state-error",
      )}
      aria-hidden
    >
      <Icon className={cn("size-9", spin && "animate-spin")} />
    </div>
  );
}

function Primary({ children, busy, ...props }: React.ComponentProps<typeof Button> & { busy?: boolean }) {
  return (
    <Button size="lg" className="h-12 w-full max-w-[20rem] rounded-full text-[0.9375rem]" autoFocus {...props}>
      {busy && <Loader2 className="animate-spin" />}
      {children}
    </Button>
  );
}

function Quiet({ children, ...props }: React.ComponentProps<typeof Button>) {
  return (
    <Button variant="ghost" size="sm" className="h-9 text-muted-foreground hover:text-foreground" {...props}>
      {children}
    </Button>
  );
}

function Problem({ text }: { text: string }) {
  return (
    <p role="alert" className="flex max-w-[22rem] items-start gap-1.5 text-left text-sm text-state-error">
      <TriangleAlert className="mt-0.5 size-4 shrink-0" />
      <span>{text}</span>
    </p>
  );
}

function sentence(s: string): string {
  const t = s.trim().replace(/\.$/, "");
  return t ? t.charAt(0).toUpperCase() + t.slice(1) + "." : "Something went wrong.";
}

/** Moves on once, a moment after `when` turns true while the step is open (not when it was true on arrival). */
function useAdvance(when: boolean, onNext: () => void) {
  const was = useRef(when);
  useEffect(() => {
    if (!when || was.current) {
      was.current = when;
      return;
    }
    was.current = true;
    const t = setTimeout(onNext, ADVANCE_AFTER);
    return () => clearTimeout(t);
  }, [when, onNext]);
}

// ------------------------------------------------------------------ Welcome

function Welcome({ onStart, onLater }: { onStart: () => void; onLater: () => void }) {
  return (
    <Screen
      art={<img src="/favicon.svg" alt="" className="size-[4.5rem] rounded-[1.375rem] shadow-sm" />}
      title="Welcome to tmx"
      text="Your AI agents work on this computer, even while you're away. Let's connect WhatsApp, your phone and Companion. It takes about two minutes."
      primary={<Primary onClick={onStart}>Get started</Primary>}
      secondary={<Quiet onClick={onLater}>Set up later</Quiet>}
    />
  );
}

// ------------------------------------------------------------------ WhatsApp

function WhatsAppStep({ onNext }: { onNext: () => void }) {
  const available = useWa((s) => s.available);
  const off = useWa((s) => s.offMessage);
  const status = useWa((s) => s.status);
  const qr = useWa((s) => s.qr);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const linked = isLinked(status);
  useAdvance(linked, onNext);

  async function link() {
    setBusy(true);
    setError("");
    try {
      await waApi.link("qr");
    } catch (e) {
      setError(sentence((e as Error).message));
    } finally {
      setBusy(false);
    }
  }
  function skip() {
    if (status?.state === "linking") void waApi.cancelLink().catch(() => {});
    onNext();
  }

  if (linked)
    return (
      <Screen
        art={<Art icon={Check} tone="ok" />}
        title="WhatsApp is connected"
        text={status?.phone ? `Linked to ${status.phone}. Your agents can now reach you there.` : "Your agents can now reach you there."}
        primary={<Primary onClick={onNext}>Continue</Primary>}
      />
    );
  if (available === false || off)
    return (
      <Screen
        art={<Art icon={MessageCircle} tone="wa" />}
        title="Talk to your agents on WhatsApp"
        text={off ? sentence(off) : "This version of tmx can't connect WhatsApp. Update tmx to use it."}
        primary={<Primary onClick={onNext}>Continue</Primary>}
      />
    );

  if (!status)
    return (
      <Screen
        art={<Art icon={MessageCircle} tone="wa" />}
        title="Talk to your agents on WhatsApp"
        primary={<Primary disabled busy>Checking…</Primary>}
        secondary={<Quiet onClick={onNext}>Not now</Quiet>}
      />
    );

  if (status?.state === "linking")
    return (
      <Screen
        art={<Art icon={MessageCircle} tone="wa" />}
        title="Scan with WhatsApp"
        compact
        primary={
          <Primary disabled busy>
            Waiting for your phone…
          </Primary>
        }
        secondary={<Quiet onClick={skip}>Not now</Quiet>}
      >
        <div className="flex size-[12rem] items-center justify-center rounded-2xl border bg-white p-2">
          {qr?.png ? <img src={qr.png} alt="Code to scan with WhatsApp" className="size-full [image-rendering:pixelated]" /> : <Loader2 className="size-6 animate-spin text-neutral-400" />}
        </div>
        <ol className="mt-4 w-full max-w-[22rem] space-y-2 text-left text-sm">
          {[
            "Open WhatsApp on your phone.",
            <>
              Tap <b className="font-medium">Settings</b>, then <b className="font-medium">Linked devices</b> (on Android: <b className="font-medium">⋮</b> then{" "}
              <b className="font-medium">Linked devices</b>).
            </>,
            <>
              Tap <b className="font-medium">Link a device</b> and point your phone at this code.
            </>,
          ].map((s, i) => (
            <li key={i} className="flex gap-3">
              <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold">{i + 1}</span>
              <span className="pt-0.5">{s}</span>
            </li>
          ))}
        </ol>
      </Screen>
    );

  const failed = status?.state === "logged_out" || status?.state === "error" || !!error;
  return (
    <Screen
      art={<Art icon={MessageCircle} tone="wa" />}
      title="Talk to your agents on WhatsApp"
      text="Message your agents from your own WhatsApp, and get their answers there, like chatting with a friend."
      primary={
        <Primary onClick={() => void link()} busy={busy} disabled={busy || !status}>
          {failed ? "Try again" : "Connect WhatsApp"}
        </Primary>
      }
      secondary={<Quiet onClick={onNext}>Not now</Quiet>}
    >
      {failed ? (
        <Problem
          text={
            error ||
            (status?.state === "logged_out" ? "WhatsApp signed this computer out. Connect it again." : status?.error ? sentence(status.error) : "That didn't work. Try again.")
          }
        />
      ) : (
        <p className="max-w-[22rem] text-xs leading-relaxed text-muted-foreground">
          WhatsApp doesn't officially support tools like this, so tmx sends messages slowly, at a person's pace. It uses your own number.
        </p>
      )}
    </Screen>
  );
}

// ------------------------------------------------------------------ Phone

// This line's pairing (docs/PHONE-PAIRING.md), not a one-scan connect: the phone opens the
// computer's HTTPS address (the code on screen is only that address), asks to pair, and then
// either types the owner PIN itself or shows a code the owner approves here, with the owner's
// proof. There is no plain-HTTP way in: without an HTTPS address the step says so and moves on.
function PhoneStep({ paired, onRead, onNext }: { paired: number | null; onRead: () => void; onNext: () => void }) {
  // Already paired when the step opened: say so; "Connect another phone" shows the code anyway.
  const [another, setAnother] = useState(false);
  // How many were paired when the step opened (null until known). One more while it is open
  // counts as just connected.
  const [had, setHad] = useState<number | null>(paired);
  useEffect(() => {
    if (had === null && paired !== null) setHad(paired);
  }, [had, paired]);
  const fresh = had !== null && paired !== null && paired > had;
  useAdvance(fresh, onNext);

  // What pairing needs here: an HTTPS address this daemon answers on, and whether the owner PIN is set.
  const [config, setConfig] = useState<Pick<ConfigView, "trusted_hosts"> | null>(null);
  const [pin, setPin] = useState<Pick<PinStatus, "available" | "set"> | null>(null);
  const connected = useStore((s) => s.conn === "open");
  useEffect(() => {
    if (!connected) return;
    let alive = true;
    onRead();
    void api.getConfig().then(
      (c) => alive && setConfig(c),
      () => alive && setConfig(null),
    );
    void api.pinStatus().then(
      (p) => alive && setPin(p),
      () => alive && setPin(null),
    );
    return () => {
      alive = false;
    };
  }, [connected, onRead]);
  const ready = paired !== null && config !== null && pin !== null;
  const address = phoneAddress(config?.trusted_hosts);
  const way = phoneWay(config, pin);
  const showing = ready && way !== "none" && !fresh && (paired === 0 || another);

  // While the code is on screen, look for the new phone every few seconds (its pairing also
  // arrives as an event; this is for a stream that dropped).
  useEffect(() => {
    if (!showing) return;
    const t = setInterval(onRead, PHONE_EVERY);
    return () => clearInterval(t);
  }, [showing, onRead]);

  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [approved, setApproved] = useState(false);
  const [error, setError] = useState("");
  async function approve() {
    setBusy(true);
    setError("");
    try {
      await api.approveDevice(code); // asks for the owner's proof on this computer
      setApproved(true);
      setCode("");
      onRead();
    } catch (e) {
      setError(sentence((e as Error).message));
    } finally {
      setBusy(false);
    }
  }

  if (!ready) return <Screen art={<Art icon={Smartphone} />} title="Use tmx on your phone" primary={<Primary disabled busy>Checking…</Primary>} />;
  if ((paired > 0 && !another) || fresh)
    return (
      <Screen
        art={<Art icon={Check} tone="ok" />}
        title="Your phone is connected"
        text="Open tmx on your phone any time to see your agents and answer them."
        primary={<Primary onClick={onNext}>Continue</Primary>}
        secondary={!fresh && way !== "none" && <Quiet onClick={() => setAnother(true)}>Connect another phone</Quiet>}
      />
    );
  if (way === "none")
    return (
      <Screen
        art={<Art icon={Smartphone} />}
        title="Use tmx on your phone"
        text="Your phone reaches tmx only through a secure address (HTTPS), and this computer doesn't have one yet. Once it does, connect your phone from Settings: Setup opens this again."
        primary={<Primary onClick={onNext}>Continue without it</Primary>}
      />
    );

  const waiting = way === "pin" || approved;
  return (
    <Screen
      art={<Art icon={Smartphone} />}
      title="Use tmx on your phone"
      compact
      primary={
        waiting ? (
          <Primary disabled busy>
            Waiting for your phone…
          </Primary>
        ) : (
          <Primary onClick={() => void approve()} busy={busy} disabled={busy || code.length !== 8} autoFocus={false}>
            Approve this phone
          </Primary>
        )
      }
      secondary={<Quiet onClick={onNext}>Not now</Quiet>}
    >
      <QRCode value={address} size={160} label="Code that opens tmx on your phone" />
      <p className="mt-2 max-w-[22rem] font-mono text-xs break-all text-muted-foreground">{address}</p>
      <ol className="mt-4 w-full max-w-[22rem] space-y-2 text-left text-sm">
        {[
          "Point your phone's camera at this code and open the link.",
          <>
            On the phone, tap <b className="font-medium">Pair this device</b>.
          </>,
          way === "pin" ? (
            <>
              Type your <b className="font-medium">owner PIN</b> on the phone. That's all.
            </>
          ) : (
            <>
              Type the <b className="font-medium">code</b> your phone shows here, then approve it.
            </>
          ),
        ].map((s, i) => (
          <li key={i} className="flex gap-3">
            <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold">{i + 1}</span>
            <span className="pt-0.5">{s}</span>
          </li>
        ))}
      </ol>
      {way === "code" && !approved && (
        <form
          className="mt-4 flex w-full max-w-[22rem] flex-col items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (!busy && code.length === 8) void approve();
          }}
        >
          <Input
            aria-label="Code on your phone"
            placeholder="Code on your phone"
            value={code}
            onChange={(e) => setCode(pairingCode(e.target.value))}
            maxLength={12}
            autoComplete="off"
            autoCapitalize="characters"
            spellCheck={false}
            disabled={busy}
            className="h-11 text-center font-mono text-lg tracking-widest"
          />
          <p className="text-xs leading-relaxed text-muted-foreground">Only approve a code you can see on your own phone.</p>
        </form>
      )}
      {approved && (
        <p className="mt-4 text-sm font-medium" role="status">
          Approved. Keep the page open on your phone; it connects in a moment.
        </p>
      )}
      {error && (
        <div className="mt-3">
          <Problem text={error} />
        </div>
      )}
    </Screen>
  );
}

// ------------------------------------------------------------------ Companion

function CompanionStep({ onNext }: { onNext: () => void }) {
  const status = useCompanion((s) => s.status);
  const h = companionHalves(status);
  const [waiting, setWaiting] = useState(false);
  const [slow, setSlow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [failed, setFailed] = useState(false);
  useAdvance(h.done, onNext);

  // Read Companion's state again every few seconds while this step is open: it ticks off by
  // itself when Companion is installed, starts, or hands tmx its key.
  useEffect(() => {
    let alive = true;
    const read = () =>
      companionApi.status(true).then(
        () => alive && setFailed(false),
        () => alive && setFailed(true),
      );
    void read();
    const t = setInterval(() => void read(), COMPANION_EVERY);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  useEffect(() => {
    if (h.done) setWaiting(false);
  }, [h.done]);
  useEffect(() => {
    if (!waiting) return;
    setSlow(false);
    const t = setTimeout(() => setSlow(true), 20_000);
    return () => clearTimeout(t);
  }, [waiting]);

  async function connect() {
    setBusy(true);
    setError("");
    try {
      const r = await companionApi.openConnect();
      if (!r.opened) setError(r.error ? sentence(r.error) : "Companion didn't open. Open it yourself, then try again.");
      else setWaiting(true);
      void companionApi.status(true).catch(() => {});
    } catch (e) {
      setError(sentence((e as Error).message));
    } finally {
      setBusy(false);
    }
  }

  const art = <Art icon={Sparkles} />;
  const title = "Connect Companion";
  const text = "Companion is your desktop assistant. Connected, it can help your agents with things on this computer, like your browser and apps.";

  if (!status)
    return (
      <Screen
        art={art}
        title={title}
        text={text}
        primary={
          failed ? (
            <Primary onClick={onNext}>Continue</Primary>
          ) : (
            <Primary disabled busy>
              Checking…
            </Primary>
          )
        }
      >
        {failed && <Problem text="Could not read Companion status." />}
      </Screen>
    );

  if (h.done)
    return (
      <Screen
        art={<Art icon={Check} tone="ok" />}
        title="Companion is connected"
        text="Companion and your agents can now work together."
        primary={<Primary onClick={onNext}>Continue</Primary>}
      >
        <Halves see ask />
      </Screen>
    );

  if (h.missing)
    return (
      <Screen
        art={art}
        title={title}
        text="Companion isn't on this computer yet. Install it and open it once; this page notices by itself."
        primary={<Primary onClick={onNext}>Continue without it</Primary>}
      >
        <Halves see={false} ask={false} />
      </Screen>
    );

  return (
    <Screen
      art={art}
      title={title}
      text={text}
      primary={
        waiting ? (
          <Primary disabled busy>
            Waiting for Companion…
          </Primary>
        ) : (
          <Primary onClick={() => void connect()} busy={busy} disabled={busy}>
            {h.refused || error ? "Connect again" : "Connect Companion"}
          </Primary>
        )
      }
      secondary={
        waiting && slow ? (
          <Quiet onClick={() => void connect()}>Didn't see it? Open it again</Quiet>
        ) : (
          <Quiet
            onClick={() => {
              setWaiting(false);
              onNext();
            }}
          >
            Not now
          </Quiet>
        )
      }
    >
      <Halves see={h.see} ask={h.ask} />
      <div className="mt-4 min-h-10">
        {error ? (
          <Problem text={error} />
        ) : h.refused ? (
          <Problem text="Companion stopped accepting tmx. Connect again to fix it." />
        ) : waiting ? (
          <p className="text-sm font-medium" role="status">
            Click <b className="font-semibold">Connect</b> in the Companion window that opens.
          </p>
        ) : h.notRunning ? (
          <p className="text-sm text-muted-foreground">Companion isn't open right now. It opens by itself when you connect.</p>
        ) : null}
      </div>
    </Screen>
  );
}

/** Companion's two links as two lines that tick off. */
function Halves({ see, ask }: { see: boolean; ask: boolean }) {
  return (
    <ul className="w-full max-w-[22rem] space-y-2 rounded-2xl bg-muted/60 px-4 py-3.5 text-left text-sm">
      <Half on={see}>Companion can see what your agents are doing</Half>
      <Half on={ask}>Your agents can ask Companion for help</Half>
    </ul>
  );
}

function Half({ on, children }: { on: boolean; children: React.ReactNode }) {
  return (
    <li className="flex items-center gap-2.5">
      <span
        className={cn(
          "flex size-5 shrink-0 items-center justify-center rounded-full transition-colors duration-300",
          on ? "bg-state-ok text-white" : "border-2 border-foreground/15",
        )}
        aria-hidden
      >
        {on && <Check className="size-3" strokeWidth={3} />}
      </span>
      <span className={on ? "" : "text-muted-foreground"}>{children}</span>
      <span className="sr-only">{on ? "(done)" : "(not yet)"}</span>
    </li>
  );
}

// ------------------------------------------------------------------ Done

function Done({
  whatsapp,
  phone,
  companion,
  onGo,
  onFinish,
}: {
  whatsapp: boolean | null;
  phone: boolean | null;
  companion: boolean | null;
  onGo: (s: OnboardStep) => void;
  onFinish: () => void;
}) {
  const rows: { step: OnboardStep; label: string; icon: React.ComponentType<{ className?: string }>; done: boolean | null }[] = [
    { step: "whatsapp", label: "WhatsApp", icon: MessageCircle, done: whatsapp },
    { step: "phone", label: "Your phone", icon: Smartphone, done: phone },
    { step: "companion", label: "Companion", icon: Sparkles, done: companion },
  ];
  return (
    <Screen
      art={<Art icon={CheckCircle2} tone="ok" />}
      title="You're all set"
      text="Here's what's ready. Anything you skipped is in Settings: Setup opens this again."
      primary={<Primary onClick={onFinish}>Start using tmx</Primary>}
    >
      <ul className="w-full max-w-[22rem] divide-y rounded-2xl border text-left">
        {rows.map((r) => (
          <li key={r.step} className="flex items-center gap-3 px-4 py-3">
            <r.icon className="size-[1.125rem] shrink-0 text-muted-foreground" />
            <span className="flex-1 text-[0.9375rem]">{r.label}</span>
            {r.done ? (
              <span className="flex items-center gap-1 text-sm font-medium text-state-ok">
                <CheckCircle2 className="size-4" />
                Connected
              </span>
            ) : r.done === null ? (
              <span className="text-sm text-muted-foreground">Status unavailable</span>
            ) : (
              <Button variant="link" size="sm" className="h-auto p-0 text-sm" onClick={() => onGo(r.step)}>
                Set up
              </Button>
            )}
          </li>
        ))}
      </ul>
    </Screen>
  );
}
