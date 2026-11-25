import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import "./lib/theme";
import App from "./App";
import { createTransport } from "./lib/transport";
import { start } from "./lib/store";
import { initPhoneLock } from "./lib/phone-lock";
import { applyTextSize, savedTextSize } from "./lib/text-size";

// The text size saved on this device, before the first render.
applyTextSize(savedTextSize(), false);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);

// A paired phone keeps the app shell (public/push-sw.js) so it can show connection recovery
// when its host is unreachable. Only on the HTTPS address.
if (window.location.protocol === "https:" && "serviceWorker" in navigator) {
  void navigator.serviceWorker.register("/push-sw.js", { scope: "/" }).catch(() => {});
}

void createTransport().then((t) => {
  initPhoneLock(t); // a paired phone: where its lock stands (lib/phone-lock.ts)
  return start(t);
});
