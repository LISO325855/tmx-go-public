// Mock of what the first-run setup reads besides WhatsApp and Companion: GET/PUT /api/config
// (the head's harness, "onboarded_at", the read-only trusted HTTPS hosts), the paired browsers
// (GET /api/devices, POST /api/devices/approve, DELETE /api/devices/{id}) and the owner PIN's
// state (GET/POST /api/gate/pin).
//
// ?mock=onboarding is a new machine: the setup opens, no phone is paired, and the computer has
// no HTTPS address yet. On any other scenario the setup was finished nine days ago, one phone is
// paired and the address is studio.example.ts.net. Switches for both: &onboarded=0 opens the
// setup, &https=1 / &https=0 gives or takes the address, &pin=1 sets the owner PIN.
import { ApiError } from "../transport";
import type { ConfigView, OwnerDevice, PinStatus } from "../types";
import { NOT_MINE } from "./resources";

const MIN = 60_000;
const TS_HOST = "studio.example.ts.net";

export class MockDevices {
  private config: ConfigView;
  private devices: OwnerDevice[];
  private pin: boolean;

  constructor(
    scenario: string,
    private emit: (type: string, data: unknown) => void,
  ) {
    const params = new URLSearchParams(typeof location === "undefined" ? "" : location.search);
    const fresh = scenario === "onboarding";
    const https = params.get("https");
    this.config = {
      head_harness: "claude",
      onboarded_at: fresh || params.get("onboarded") === "0" ? 0 : Date.now() - 9 * 24 * 60 * MIN,
      trusted_hosts: https === "1" || (!fresh && https !== "0") ? [TS_HOST] : [],
    };
    this.devices = fresh ? [] : [{ id: "d1", name: "My phone", host: TS_HOST }];
    this.pin = params.get("pin") === "1";
  }

  route(method: string, parts: string[], body: unknown): unknown {
    const [res, id] = parts;
    if (res === "config" && !id) {
      if (method === "PUT") {
        const b = (body ?? {}) as ConfigView;
        if (b.head_harness) this.config.head_harness = b.head_harness;
        // as the daemon: any number but 0 means "now", and a second "done" keeps the first time
        if (b.onboarded_at !== undefined && !b.onboarded_at !== !this.config.onboarded_at) this.config.onboarded_at = b.onboarded_at ? Date.now() : 0;
      }
      return this.config;
    }
    if (res === "devices") {
      if (!id && method === "GET") return this.devices;
      if (id === "approve" && method === "POST") {
        const code = String((body as { code?: string })?.code ?? "").trim().toUpperCase();
        if (!/^[A-Z2-7]{8}$/.test(code)) throw new ApiError(400, "invalid or expired pairing code");
        // the phone claims its approved pairing a moment later (its page asks every two seconds)
        setTimeout(() => {
          const d: OwnerDevice = { id: "d" + Math.random().toString(36).slice(2, 8), name: "My phone", host: this.config.trusted_hosts?.[0] ?? TS_HOST };
          this.devices.push(d);
          this.emit("device.paired", { id: d.id });
        }, 1200);
        return { approved: true };
      }
      if (id && method === "DELETE") {
        this.devices = this.devices.filter((d) => d.id !== id);
        this.emit("device.revoked", { id });
        return { revoked: true };
      }
      return NOT_MINE;
    }
    if (res === "gate" && id === "pin" && !parts[2]) {
      if (method === "POST") {
        this.pin = true;
        return { set: true };
      }
      const st: PinStatus = { available: true, set: this.pin, at_machine: true, wait_s: 0 };
      return st;
    }
    return NOT_MINE;
  }
}
