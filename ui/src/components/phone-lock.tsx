// The phone lock's screens (lib/phone-lock.ts; docs/PHONE-PAIRING.md):
//   <PhoneLock />           over the whole app on a paired phone: blank while the page is in the
//                           background, the lock screen while locked, the fingerprint offer once;
//   <PhoneLockSetting />    at the computer, Settings → Owner devices: on/off and "Lock after";
//   <PhoneLockOnPhone />    on the phone, Settings → Owner devices: lock now, the fingerprint.
import { useCallback, useEffect, useRef, useState } from "react";
import { Fingerprint, Loader2, Lock } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { PinInput, validPin } from "@/components/pin-input";
import { api } from "@/lib/api";
import { lockNow, platformAuthenticator, removeFace, setUpFace, skipFace, unlockWithFace, unlockWithPin, useLock } from "@/lib/phone-lock";
import { biometricName, lockActive, lockAfterLabel, unlockProblem, waitSeconds, type LockSetting } from "@/lib/phone-lock-core";
import { ApiError } from "@/lib/transport";

const bio = () => biometricName(typeof navigator === "undefined" ? "" : navigator.userAgent);
const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** What went wrong with the fingerprint, in plain words ("" = the person just cancelled). */
function faceProblem(e: unknown): string {
  const name = (e as { name?: string })?.name;
  if (name === "NotAllowedError" || name === "AbortError") return "";
  if (e instanceof ApiError) return cap(e.message) + (e.message.endsWith(".") ? "" : ".");
  return `${cap(bio())} didn't work. Use your PIN.`;
}

export function PhoneLock() {
  const phase = useLock((s) => s.phase);
  const covered = useLock((s) => s.covered);
  if (phase !== "locked" && phase !== "face-offer" && !covered) return null;
  return (
    <div className="fixed inset-0 z-[1000] flex items-center justify-center bg-background p-6" data-phone-lock={phase}>
      {phase === "locked" ? <LockScreen /> : phase === "face-offer" ? <FaceOffer /> : null}
    </div>
  );
}

function LockScreen() {
  const status = useLock((s) => s.status);
  const prompt = useLock((s) => s.prompt);
  const [pin, setPin] = useState("");
  const [busy, setBusy] = useState(false);
  const [faceBusy, setFaceBusy] = useState(false);
  const faceRun = useRef(0);
  const [error, setError] = useState("");
  const [waitUntil, setWaitUntil] = useState(() => (status?.retry_after_ms ? Date.now() + status.retry_after_ms : 0));
  const [now, setNow] = useState(Date.now());
  const waiting = waitSeconds(waitUntil - now);

  useEffect(() => {
    if (!waitUntil) return;
    const t = setInterval(() => setNow(Date.now()), 500);
    return () => clearInterval(t);
  }, [waitUntil]);

  const face = useCallback(async (automatic = false) => {
    const run = ++faceRun.current;
    setFaceBusy(true);
    setError("");
    try {
      await unlockWithFace(automatic);
    } catch (e) {
      if (faceRun.current === run) setError(faceProblem(e));
    } finally {
      if (faceRun.current === run) setFaceBusy(false);
    }
  }, []);

  // The fingerprint is offered by itself when the lock screen opens or the page comes back.
  const offered = useRef(0);
  useEffect(() => {
    if (busy || !status?.passkey || offered.current === prompt || document.visibilityState !== "visible") return;
    offered.current = prompt;
    void face(true);
  }, [prompt, status?.passkey, face, busy]);

  async function submit() {
    ++faceRun.current;
    setFaceBusy(false);
    setBusy(true);
    setError("");
    try {
      await unlockWithPin(pin);
    } catch (e) {
      const p = unlockProblem(e instanceof ApiError ? e.status : -1, (e as Error).message, Number((e as ApiError).data?.wait_s) || 0);
      setError(p.text);
      setWaitUntil(p.waitMs ? Date.now() + p.waitMs : 0);
      setNow(Date.now());
    } finally {
      setPin("");
      setBusy(false);
    }
  }

  return (
    <form
      className="flex w-full max-w-sm flex-col items-center text-center"
      onSubmit={(e) => {
        e.preventDefault();
        if (validPin(pin) && !busy && !waiting) void submit();
      }}
    >
      <div className="flex size-14 items-center justify-center rounded-full bg-muted">
        <Lock className="size-7" />
      </div>
      <h1 className="mt-4 text-xl font-semibold">tmx is locked</h1>
      <p className="mt-1.5 text-sm text-muted-foreground">{status?.passkey ? `Use your ${bio()} or type your owner PIN.` : "Type your owner PIN."}</p>
      {status?.passkey && (
        <Button type="button" size="lg" variant="outline" className="mt-5 w-full" disabled={busy || faceBusy} onClick={() => void face()}>
          <Fingerprint />
          Unlock with {bio()}
        </Button>
      )}
      <div className="mt-5 w-full">
        <PinInput label="Owner PIN" value={pin} onChange={setPin} autoFocus={!status?.passkey} disabled={busy || waiting > 0} />
      </div>
      {(error || waiting > 0) && (
        <p role="alert" className="mt-3 text-sm text-state-error">
          {waiting > 0 ? `Too many wrong PINs. Try again in ${waiting} s.` : error}
        </p>
      )}
      <Button type="submit" size="lg" className="mt-5 w-full" disabled={busy || waiting > 0 || !validPin(pin)}>
        {busy && <Loader2 className="animate-spin" />}
        Unlock
      </Button>
    </form>
  );
}

/** Once after pairing, where the phone can do it: unlock with the body next time? */
function FaceOffer() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function use() {
    setBusy(true);
    setError("");
    try {
      await setUpFace();
    } catch (e) {
      setError(faceProblem(e) || "Not set up. You can do it later in Settings.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="flex w-full max-w-sm flex-col items-center text-center">
      <div className="flex size-14 items-center justify-center rounded-full bg-muted">
        <Fingerprint className="size-7" />
      </div>
      <h1 className="mt-4 text-xl font-semibold">Unlock with your {bio()}?</h1>
      <p className="mt-1.5 text-sm text-muted-foreground">
        tmx locks itself when you put your phone away. With your {bio()} you open it again in one touch; your owner PIN always works too.
      </p>
      {error && (
        <p role="alert" className="mt-3 text-sm text-state-error">
          {error}
        </p>
      )}
      <Button size="lg" className="mt-6 w-full" disabled={busy} onClick={() => void use()}>
        {busy ? <Loader2 className="animate-spin" /> : <Fingerprint />}
        Use my {bio()}
      </Button>
      <Button size="lg" variant="ghost" className="mt-2 w-full" disabled={busy} onClick={skipFace}>
        Not now
      </Button>
    </div>
  );
}

/** At the computer: the lock for every paired phone. */
export function PhoneLockSetting() {
  const [setting, setSetting] = useState<LockSetting | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  useEffect(() => {
    let live = true;
    api.phoneLock().then(
      (s) => live && setSetting(s),
      () => {}, // an older daemon: no row
    );
    return () => {
      live = false;
    };
  }, []);
  if (!setting) return null;
  async function change(next: { enabled: boolean; after_seconds: number }) {
    setBusy(true);
    setMessage("");
    try {
      setSetting(await api.setPhoneLock(next));
    } catch (e) {
      setMessage(cap((e as Error).message));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-2 rounded border p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="font-medium">Lock tmx on my phones</div>
          <div className="text-sm text-muted-foreground">
            When a phone is put away, tmx on it locks. You open it with your fingerprint or your owner PIN.
            {!setting.pin_set && " It starts working once you have an owner PIN."}
          </div>
        </div>
        <Switch aria-label="Lock tmx on my phones" checked={setting.enabled} disabled={busy} onCheckedChange={(v) => void change({ enabled: v, after_seconds: setting.after_seconds })} />
      </div>
      {setting.enabled && (
        <div className="flex items-center justify-between gap-3">
          <div className="text-sm">Lock after</div>
          <Select value={String(setting.after_seconds)} disabled={busy} onValueChange={(v) => void change({ enabled: true, after_seconds: Number(v) })}>
            <SelectTrigger className="w-40" aria-label="Lock after">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {(setting.choices ?? [setting.after_seconds]).map((s) => (
                <SelectItem key={s} value={String(s)}>
                  {lockAfterLabel(s)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}
      {message && (
        <p role="alert" className="text-sm text-state-error">
          {message}
        </p>
      )}
    </div>
  );
}

/** On the phone: lock now, and the fingerprint for this phone. */
export function PhoneLockOnPhone() {
  const status = useLock((s) => s.status);
  const [canFace, setCanFace] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  useEffect(() => {
    void platformAuthenticator().then(setCanFace);
  }, []);
  if (!lockActive(status)) return null;
  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setMessage("");
    try {
      await fn();
    } catch (e) {
      setMessage(faceProblem(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-2 rounded border p-3">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="font-medium">Lock on this phone</div>
          <div className="text-sm text-muted-foreground">Locks {status!.after_seconds <= 0 ? "as soon as" : `${lockAfterLabel(status!.after_seconds)} after`} you leave tmx. Change it on your computer.</div>
        </div>
        <Button variant="outline" disabled={busy} onClick={() => void run(lockNow)}>
          <Lock />
          Lock now
        </Button>
      </div>
      {status!.passkey_possible && (status!.passkey || canFace) && (
        <div className="flex items-center justify-between gap-3">
          <div className="text-sm">{status!.passkey ? `Your ${bio()} unlocks tmx on this phone.` : `Unlock with your ${bio()} instead of the PIN.`}</div>
          {status!.passkey ? (
            <Button variant="outline" disabled={busy} onClick={() => void run(removeFace)}>
              Remove
            </Button>
          ) : (
            <Button variant="outline" disabled={busy} onClick={() => void run(setUpFace)}>
              <Fingerprint />
              Use my {bio()}
            </Button>
          )}
        </div>
      )}
      {message && (
        <p role="alert" className="text-sm text-state-error">
          {message}
        </p>
      )}
    </div>
  );
}
