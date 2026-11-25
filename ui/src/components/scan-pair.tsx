// The phone's side of "Connect a phone" (docs/PHONE-PAIRING.md): the camera opened
// https://<this computer>/#pair=<code>. One screen: the owner PIN. Code + PIN go in one
// request; the answer sets this browser's own session and the page opens tmx.
import { useEffect, useState } from "react";
import { CheckCircle2, Loader2, Smartphone, TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PinInput, validPin } from "@/components/pin-input";
import { checkScanCode, deviceName, ScanError, scanPair, scanProblem } from "@/lib/phone-connect";

type View = "checking" | "pin" | "dead" | "done";

export function ScanPair({ code }: { code: string }) {
  const [view, setView] = useState<View>("checking");
  const [pin, setPin] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const check = () => {
    setView("checking");
    setError("");
    checkScanCode(code).then(
      (r) => {
        if (r.pin_set) setView("pin");
        else {
          setError(scanProblem(new ScanError(409, "")).text);
          setView("dead");
        }
      },
      (e) => {
        const p = scanProblem(e);
        setError(p.text);
        setView(p.retry ? "pin" : "dead");
      },
    );
  };
  useEffect(check, [code]);

  async function submit() {
    setBusy(true);
    setError("");
    try {
      await scanPair(code, deviceName(navigator.userAgent), pin);
      setView("done");
      setTimeout(() => window.location.reload(), 900); // the code already left the address (takePairCode)
    } catch (e) {
      const p = scanProblem(e);
      setPin("");
      setError(p.text);
      if (!p.retry) setView("dead");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex w-full max-w-sm flex-col items-center rounded-xl border p-6 text-center">
      {view === "done" ? (
        <div role="status" className="flex flex-col items-center py-4">
          <CheckCircle2 className="size-12 text-state-ok" />
          <h1 className="mt-4 text-xl font-semibold">Connected</h1>
          <p className="mt-1 text-sm text-muted-foreground">Opening tmx…</p>
        </div>
      ) : view === "dead" ? (
        <div role="alert" className="flex flex-col items-center py-2">
          <TriangleAlert className="size-10 text-state-error" />
          <h1 className="mt-4 text-xl font-semibold">Scan a new code</h1>
          <p className="mt-2 text-sm text-muted-foreground">{error}</p>
        </div>
      ) : view === "checking" ? (
        <div role="status" className="flex flex-col items-center py-6">
          <Loader2 className="size-8 animate-spin text-muted-foreground" />
          <p className="mt-4 text-sm text-muted-foreground">Reaching your computer…</p>
        </div>
      ) : (
        <form
          className="flex w-full flex-col items-center"
          onSubmit={(e) => {
            e.preventDefault();
            if (validPin(pin) && !busy) void submit();
          }}
        >
          <div className="flex size-12 items-center justify-center rounded-full bg-muted">
            <Smartphone className="size-6" />
          </div>
          <h1 className="mt-4 text-xl font-semibold">Connect this phone</h1>
          <p className="mt-1.5 text-sm text-muted-foreground">Type your owner PIN.</p>
          <div className="mt-5 w-full">
            <PinInput label="Owner PIN" value={pin} onChange={setPin} autoFocus disabled={busy} />
          </div>
          {error && (
            <p role="alert" className="mt-3 text-sm text-state-error">
              {error}
            </p>
          )}
          <Button type="submit" size="lg" className="mt-5 w-full" disabled={busy || !validPin(pin)}>
            {busy && <Loader2 className="animate-spin" />}
            Connect
          </Button>
        </form>
      )}
    </div>
  );
}
