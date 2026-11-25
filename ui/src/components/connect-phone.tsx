// "Connect a phone" at the computer (docs/PHONE-PAIRING.md): one dialog. It walks through
// whatever is still missing, one step per screen (owner PIN, Tailscale on this computer and
// on the phone), checking again by itself every few seconds, then shows a code the phone's
// camera opens. The phone asks for the owner PIN and is in (components/scan-pair.tsx).
// Opened from Settings → Owner devices and the command palette (ui.openConnectPhone); the
// first-run wizard embeds <ConnectPhone embedded />.
// Ported from Mustneer's v0.1.20 connect-phone.tsx onto our pairing.
import { useCallback, useEffect, useRef, useState } from "react";
import { Check, CheckCircle2, Download, ExternalLink, Globe, KeyRound, Loader2, LogIn, ShieldCheck, Smartphone, TriangleAlert, Wifi } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { CopyButton } from "@/components/copy-button";
import { QRCode } from "@/components/chat/qr-code";
import { PinInput, PIN_MIN, PIN_MAX, validPin } from "@/components/pin-input";
import { api } from "@/lib/api";
import { Switch } from "@/components/ui/switch";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { allowHint, plainError, problemText, SETUP_STEPS, setupIndex, setupStep, TAILSCALE_DOWNLOAD, phoneButtonLabel, phonesConnected, remotePage, wifiStep, type PhoneCode, type PhoneStatus, type PhoneWay, type SetupStep, type WifiStep } from "@/lib/phone-connect";
import { ApiError } from "@/lib/transport";
import { onBusEvent, useStore } from "@/lib/store";
import { ui, useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";

const CHECK_EVERY = 4000;

export function ConnectPhoneDialog() {
  const open = useUI((s) => s.connectPhone);
  return (
    <Dialog open={open} onOpenChange={(v) => ui.openConnectPhone(v)}>
      <DialogContent className="sm:max-w-[26rem]">{open && <ConnectPhone />}</DialogContent>
    </Dialog>
  );
}

function openLink(url: string) {
  const tr = useStore.getState().transport;
  if (tr) tr.openExternal(url);
  else window.open(url, "_blank", "noopener");
}

/** The phone in the top bar (as in the Python tmx): one click opens Connect a phone, and a
 *  green dot says at a glance that a phone is connected. Only in the computer's own window:
 *  a paired phone cannot connect other devices. */
export function ConnectPhoneButton({ className }: { className?: string }) {
  const desktop = useStore((s) => !!s.transport?.desktop);
  const ready = useStore((s) => s.status === "ready");
  const [count, setCount] = useState(0);
  const here = typeof window !== "undefined" && !remotePage(window.location.hostname, desktop);
  useEffect(() => {
    if (!here || !ready) return;
    let live = true;
    const load = () =>
      void api.devices().then(
        (list) => live && setCount(phonesConnected(list)),
        () => {}, // an older daemon, or not the owner: no dot
      );
    load();
    const off = onBusEvent((ev) => {
      if ((ev.type as string).startsWith("device.")) load();
    });
    return () => {
      live = false;
      off();
    };
  }, [here, ready]);
  if (!here) return null;
  const label = phoneButtonLabel(count);
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon-sm" className={cn("relative text-muted-foreground", className)} aria-label={label} data-phones={count} onClick={() => ui.openConnectPhone(true)}>
          <Smartphone className="size-4" />
          {count > 0 && <span className="absolute right-1 top-1 size-2 rounded-full bg-state-ok ring-2 ring-sidebar" aria-hidden />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

/** The flow itself. embedded: inside another dialog (the first-run wizard), which has its own
 *  title and moves on by itself: onConnected gets the phone's name instead of this closing. */
export function ConnectPhone({ embedded = false, onConnected }: { embedded?: boolean; onConnected?: (name: string) => void } = {}) {
  const [status, setStatus] = useState<PhoneStatus | null>(null);
  const [phoneDone, setPhoneDone] = useState(false);
  const [code, setCode] = useState<PhoneCode | null>(null);
  const [error, setError] = useState("");
  const [phone, setPhone] = useState<string | null>(null); // the phone that connected
  const [now, setNow] = useState(Date.now());
  const asking = useRef(false);
  // The way: Tailscale (home and away, offered first) or the same Wi-Fi fallback.
  const [way, setWay] = useState<PhoneWay>("tailscale");
  const [addr, setAddr] = useState(0); // which of this computer's addresses the Wi-Fi code uses
  const checked = status !== null;
  const step: SetupStep | WifiStep = !status ? "finish" : way === "wifi" ? wifiStep(status) : setupStep(status, phoneDone);

  const check = useCallback(async () => {
    try {
      // Only the Tailscale way lets tmx set Tailscale up while it looks.
      setStatus(await api.phone(way === "tailscale"));
    } catch {
      setStatus(null);
    }
  }, [way]);

  const switchWay = useCallback((next: PhoneWay) => {
    void api.cancelPhoneCode().catch(() => {});
    setCode(null);
    setError("");
    setAddr(0);
    setStatus(null);
    setWay(next);
  }, []);

  // Until a code shows, look again every few seconds: each step notices by itself when it is done.
  useEffect(() => {
    if (code) return;
    void check();
    const t = setInterval(() => void check(), CHECK_EVERY);
    return () => clearInterval(t);
  }, [check, code]);

  // The code stops working when the dialog closes.
  useEffect(() => () => void api.cancelPhoneCode().catch(() => {}), []);

  const newCode = useCallback(async (address?: string) => {
    if (asking.current) return;
    asking.current = true;
    setError("");
    try {
      const c = await api.connectPhone(way, way === "wifi" ? address : undefined);
      setCode(c);
      setNow(Date.now());
    } catch (e) {
      setCode(null);
      setError(plainError((e as Error).message));
    } finally {
      asking.current = false;
    }
  }, [way]);

  // Everything is in place: get the code (tmx may ask the owner once, to trust the new address).
  useEffect(() => {
    if (checked && step === "finish" && !code && !error) void newCode();
  }, [checked, step, code, error, newCode]);

  // The phone scanned it: a device paired while the code was on screen.
  useEffect(() => {
    if (!code) return;
    const tick = setInterval(() => setNow(Date.now()), 1000);
    const off = onBusEvent((ev) => {
      if ((ev.type as string) !== "device.paired") return;
      const id = (ev.data as { id?: string } | undefined)?.id;
      void api.devices().then(
        (list) => setPhone(list.find((d) => d.id === id)?.name ?? ""),
        () => setPhone(""),
      );
    });
    return () => {
      clearInterval(tick);
      off();
    };
  }, [code]);

  const told = useRef(false);
  useEffect(() => {
    if (phone === null) return;
    if (embedded) {
      if (!told.current) onConnected?.(phone);
      told.current = true;
      return;
    }
    const t = setTimeout(() => {
      ui.openConnectPhone(false);
      toast.success(`${phone || "Your phone"} is connected.`);
    }, 2200);
    return () => clearTimeout(t);
  }, [phone, embedded, onConnected]);

  if (phone !== null) return <Done name={phone} embedded={embedded} />;

  const wayLink =
    way === "tailscale" ? (
      <Button variant="link" size="sm" className="h-auto p-0 text-xs text-muted-foreground" onClick={() => switchWay("wifi")}>
        <Wifi />
        Tailscale doesn't work on the phone? Use the same Wi-Fi
      </Button>
    ) : (
      <Button variant="link" size="sm" className="h-auto p-0 text-xs text-muted-foreground" onClick={() => switchWay("tailscale")}>
        <Globe />
        Use the way that also works away from home
      </Button>
    );

  if (code) {
    const expired = code.pairing.expires_at <= now;
    const addresses = status?.wifi.addresses ?? [];
    return (
      <div className="flex flex-col items-center text-center">
        {!embedded && (
          <>
            <DialogTitle className="text-lg">Connect a phone</DialogTitle>
            <DialogDescription className="sr-only">Scan the code with your phone's camera, then type your owner PIN on the phone.</DialogDescription>
          </>
        )}
        <div className={cn("flex items-center justify-center", embedded ? "size-[13rem]" : "mt-5 size-[15.75rem]")}>
          {expired ? (
            <div className="flex size-full flex-col items-center justify-center gap-3 rounded-xl border border-dashed text-sm text-muted-foreground">
              This code has expired.
              <Button onClick={() => void newCode(addresses[addr]?.ip)}>New code</Button>
            </div>
          ) : (
            <QRCode value={code.pairing.url} size={embedded ? 188 : 232} label="Code for your phone's camera" />
          )}
        </div>
        <p className="mt-5 text-[0.9375rem] font-medium">Open your phone's camera and point it here.</p>
        <p className="mt-1 text-sm text-muted-foreground">
          {code.via === "wifi" ? "Your phone must be on the same Wi-Fi as this computer. Then type your owner PIN on the phone." : "Then type your owner PIN on the phone. Works at home and away."}
        </p>
        {code.via === "tailscale" && !status?.tailscale.phone_online && (
          <p className="mt-3 text-xs text-muted-foreground">
            The page doesn't open on the phone? Open the Tailscale app there and switch it on, then scan again.
          </p>
        )}
        <div className="mt-4 flex flex-col items-center gap-1.5">
          {code.via === "wifi" && addresses.length > 1 && (
            <Button
              variant="link"
              size="sm"
              className="h-auto p-0 text-xs text-muted-foreground"
              onClick={() => {
                const next = (addr + 1) % addresses.length;
                setAddr(next);
                void newCode(addresses[next].ip);
              }}
            >
              Phone can't open it? Try this computer's other address
            </Button>
          )}
          {wayLink}
        </div>
      </div>
    );
  }

  let body: React.ReactNode;
  switch (step) {
    case "pin":
      body = <ChoosePin onDone={() => void check()} />;
      break;
    case "install":
      body = (
        <Step
          icon={Download}
          title="Install the free Tailscale app on this computer"
          text="It connects your phone to this computer safely, wherever you are."
          action={
            <Button size="lg" className="w-full" onClick={() => openLink(TAILSCALE_DOWNLOAD)}>
              <ExternalLink />
              Download Tailscale
            </Button>
          }
          note="Come back here when it's installed. This page notices by itself."
        />
      );
      break;
    case "signin":
      body = <SignIn onChecked={() => void check()} />;
      break;
    case "allow":
      body = (
        <Step
          icon={ShieldCheck}
          title="Allow secure connections"
          text={allowHint(status)}
          action={
            <Button size="lg" className="w-full" onClick={() => status?.tailscale.link && openLink(status.tailscale.link)} disabled={!status?.tailscale.link}>
              <ExternalLink />
              Open the page
            </Button>
          }
        />
      );
      break;
    case "phone":
      body = (
        <Step
          icon={Smartphone}
          title="Install Tailscale on your phone"
          text="Scan this with your phone's camera, install the app, sign in with the same account as on this computer, and switch it on."
          extra={<QRCode value={TAILSCALE_DOWNLOAD} size={136} label="Tailscale for your phone" className="mt-4" />}
          action={
            <Button size="lg" className="w-full" onClick={() => setPhoneDone(true)}>
              <Check />
              Done
            </Button>
          }
          note="This page notices by itself when your phone is on."
        />
      );
      break;
    case "switch":
      body = <WifiSwitch onDone={() => void check()} />;
      break;
    case "network":
      body = (
        <Step
          icon={TriangleAlert}
          tone="error"
          title="This computer isn't on a network"
          text="Connect it to your Wi-Fi or a network cable. This page notices by itself."
        />
      );
      break;
    case "finish":
      body = error ? (
        <Step
          icon={TriangleAlert}
          tone="error"
          title="That didn't work"
          text={error}
          action={
            <Button size="lg" className="w-full" onClick={() => setError("")}>
              Try again
            </Button>
          }
        />
      ) : (
        <Step icon={Loader2} spin title={checked ? "Almost there" : "Connect a phone"} text={checked ? "Getting your code ready…" : "Checking this computer…"} />
      );
      break;
    default: {
      const p = problemText(status);
      body = (
        <Step
          icon={TriangleAlert}
          tone="error"
          title={p.title}
          text={p.text}
          extra={
            p.command && (
              <div className="mt-3 flex items-center gap-1 rounded-md bg-muted px-2 py-1.5">
                <code className="font-mono text-xs">{p.command}</code>
                <CopyButton text={p.command} label="Copy" />
              </div>
            )
          }
          action={
            <Button size="lg" className="w-full" onClick={() => void check()}>
              Try again
            </Button>
          }
        />
      );
    }
  }

  const index = way === "tailscale" ? setupIndex(step as SetupStep) : 0;
  const guided = checked && step !== "finish" && way === "tailscale";
  const stepping = checked && step !== "finish";
  return (
    <div className="flex flex-col">
      {!embedded && (
        <>
          <DialogTitle className={stepping ? "text-sm font-medium text-muted-foreground" : "sr-only"}>Connect a phone</DialogTitle>
          <DialogDescription className="sr-only">Set up once so your phone reaches tmx at home and away.</DialogDescription>
        </>
      )}
      {guided && (
        <div className="mt-2 flex justify-center gap-1.5" role="img" aria-label={`Step ${index + 1} of ${SETUP_STEPS.length}`}>
          {SETUP_STEPS.map((s, i) => (
            <span key={s} className={cn("h-1.5 w-8 rounded-full", i < index ? "bg-state-ok" : i === index ? "bg-foreground" : "bg-muted")} />
          ))}
        </div>
      )}
      <div className="mt-6">{body}</div>
      {stepping && step !== "pin" && (
        <div className="mt-4 flex flex-col items-center gap-1.5">
          {guided && (
            <Button variant="link" size="sm" className="h-auto p-0 text-xs text-muted-foreground" onClick={() => void check()}>
              Check again
            </Button>
          )}
          {wayLink}
        </div>
      )}
    </div>
  );
}

/** The same Wi-Fi fallback is off: what it is, and the switch. A firewall in the way says so. */
function WifiSwitch({ onDone }: { onDone: () => void }) {
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<{ text: string; command?: string } | null>(null);
  async function on() {
    setBusy(true);
    setProblem(null);
    try {
      await api.setPhoneWifi(true);
      onDone();
    } catch (e) {
      const data = e instanceof ApiError ? e.data : undefined;
      setProblem({ text: plainError((e as Error).message) + (data?.step === "firewall" ? ":" : ""), command: data?.step === "firewall" && typeof data.output === "string" ? data.output : undefined });
      if (data?.step === "firewall") onDone(); // it is on; only the firewall is in the way
    } finally {
      setBusy(false);
    }
  }
  return (
    <Step
      icon={Wifi}
      title="Connect over the same Wi-Fi"
      text="For when Tailscale doesn't work on your phone. It only works while the phone is on the same Wi-Fi as this computer, and without the secure connection the phone has no dictation, no notifications and no fingerprint unlock. You can switch it off again in Settings."
      extra={
        problem && (
          <div role="alert" className="mt-3 text-sm text-state-error">
            {problem.text}
            {problem.command && (
              <div className="mt-2 flex items-center gap-1 rounded-md bg-muted px-2 py-1.5 text-foreground">
                <code className="break-all font-mono text-xs">{problem.command}</code>
                <CopyButton text={problem.command} label="Copy" />
              </div>
            )}
          </div>
        )
      }
      action={
        <Button size="lg" className="w-full" onClick={() => void on()} disabled={busy}>
          {busy ? <Loader2 className="animate-spin" /> : <Wifi />}
          {problem ? "Try again" : "Switch on"}
        </Button>
      }
    />
  );
}

/** Settings → Owner devices, at the computer: the same Wi-Fi fallback's switch. */
export function WifiFallbackSetting() {
  const [st, setSt] = useState<PhoneStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  useEffect(() => {
    let live = true;
    api.phone().then(
      (s) => live && setSt(s),
      () => {}, // an older daemon: no row
    );
    return () => {
      live = false;
    };
  }, []);
  if (!st) return null;
  async function change(on: boolean) {
    setBusy(true);
    setMessage("");
    try {
      const wifi = await api.setPhoneWifi(on);
      setSt((s) => (s ? { ...s, wifi } : s));
    } catch (e) {
      const data = e instanceof ApiError ? e.data : undefined;
      setMessage(plainError((e as Error).message) + (typeof data?.output === "string" && data.output ? `: ${data.output}` : ""));
      void api.phone().then(setSt, () => {});
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-2 rounded border p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="font-medium">Connect over the same Wi-Fi (fallback)</div>
          <div className="text-sm text-muted-foreground">
            For a phone that can't use Tailscale: tmx also answers on your home network, without the secure connection. Off unless you need it. Switching it off disconnects the phones
            connected this way.
          </div>
        </div>
        <Switch aria-label="Connect over the same Wi-Fi" checked={st.wifi.on} disabled={busy} onCheckedChange={(v) => void change(v)} />
      </div>
      {message && (
        <p role="alert" className="break-words text-sm text-state-error">
          {message}
        </p>
      )}
    </div>
  );
}

/** The owner PIN, chosen here when there is none yet: the phone asks for it. */
function ChoosePin({ onDone }: { onDone: () => void }) {
  const [pin, setPin] = useState("");
  const [again, setAgain] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const ready = validPin(pin) && pin === again;
  async function save() {
    setBusy(true);
    setError("");
    try {
      await api.setPin(pin);
      onDone();
    } catch (e) {
      setError(plainError((e as Error).message));
    } finally {
      setBusy(false);
    }
  }
  return (
    <form
      className="flex flex-col items-center text-center"
      onSubmit={(e) => {
        e.preventDefault();
        if (ready && !busy) void save();
      }}
    >
      <div className="flex size-12 items-center justify-center rounded-full bg-muted">
        <KeyRound className="size-6" />
      </div>
      <h3 className="mt-4 text-[1.0625rem] font-semibold">Choose your owner PIN</h3>
      <p className="mt-1.5 text-sm text-muted-foreground">
        {PIN_MIN} to {PIN_MAX} digits. Your phone asks for it when it connects, and when something needs your OK.
      </p>
      <div className="mt-5 w-full space-y-2">
        <PinInput label="Owner PIN" value={pin} onChange={setPin} autoFocus disabled={busy} />
        <PinInput label="Owner PIN again" value={again} onChange={setAgain} disabled={busy} />
      </div>
      {again.length >= pin.length && pin !== again && validPin(pin) && <p className="mt-2 text-xs text-state-error">The two don't match.</p>}
      {error && (
        <p role="alert" className="mt-2 text-xs text-state-error">
          {error}
        </p>
      )}
      <Button type="submit" size="lg" className="mt-6 w-full" disabled={!ready || busy}>
        {busy ? <Loader2 className="animate-spin" /> : <Check />}
        Save PIN
      </Button>
    </form>
  );
}

function SignIn({ onChecked }: { onChecked: () => void }) {
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  async function signIn() {
    setBusy(true);
    try {
      const r = await api.tailscaleSignIn();
      if (r.link) {
        openLink(r.link);
        setNote("Sign in on the page that opened. This page notices by itself.");
      } else if (r.opened) setNote("Tailscale opened. Sign in there; this page notices by itself.");
      else onChecked();
    } catch (e) {
      setNote(plainError((e as Error).message));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Step
      icon={LogIn}
      title="Sign in to Tailscale"
      text="Any account works: Google, Apple or Microsoft. Use the same one on your phone later."
      action={
        <Button size="lg" className="w-full" onClick={() => void signIn()} disabled={busy}>
          {busy ? <Loader2 className="animate-spin" /> : <LogIn />}
          Sign in
        </Button>
      }
      note={note}
    />
  );
}

function Done({ name, embedded }: { name: string; embedded?: boolean }) {
  const Title = embedded ? "h3" : DialogTitle;
  const Text = embedded ? "p" : DialogDescription;
  return (
    <div className="flex flex-col items-center py-6 text-center" role="status">
      <div className="flex size-16 items-center justify-center rounded-full bg-state-ok/15">
        <CheckCircle2 className="size-9 text-state-ok" />
      </div>
      <Title className="mt-4 text-lg font-semibold">Your phone is connected</Title>
      <Text className="mt-1 text-sm text-muted-foreground">{name ? `${name} can use tmx now.` : "It can use tmx now."}</Text>
    </div>
  );
}

function Step({
  icon: Icon,
  title,
  text,
  action,
  note,
  extra,
  tone,
  spin,
}: {
  icon: React.ComponentType<{ className?: string }>;
  title: string;
  text: string;
  action?: React.ReactNode;
  note?: string;
  extra?: React.ReactNode;
  tone?: "error";
  spin?: boolean;
}) {
  return (
    <div className="flex flex-col items-center text-center">
      <div className={cn("flex size-12 items-center justify-center rounded-full", tone === "error" ? "bg-state-error/10" : "bg-muted")}>
        <Icon className={cn("size-6", tone === "error" ? "text-state-error" : "text-foreground", spin && "animate-spin")} />
      </div>
      <h3 className="mt-4 text-[1.0625rem] font-semibold">{title}</h3>
      <p className="mt-1.5 text-sm text-muted-foreground">{text}</p>
      {extra}
      {action && <div className="mt-6 w-full">{action}</div>}
      {note && <p className="mt-3 text-xs text-muted-foreground">{note}</p>}
    </div>
  );
}
