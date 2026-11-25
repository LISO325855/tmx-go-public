// The Node bundle for scripts/render-check.mjs: the whole UI, every dependency inlined, with
// zustand wrapped so a render to a string sees the current state (zustand-ssr.ts).
import path from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const ui = path.resolve(import.meta.dirname, "../..");
export default defineConfig({
  root: ui,
  plugins: [react()],
  logLevel: "error",
  publicDir: false,
  resolve: {
    alias: [
      { find: /^zustand$/, replacement: path.join(ui, "scripts/render/zustand-ssr.ts") },
      { find: "@", replacement: path.join(ui, "src") },
    ],
  },
  ssr: { noExternal: true },
  build: { ssr: path.join(ui, "scripts/render/entry.tsx"), outDir: path.join(ui, "scripts/render/out"), emptyOutDir: true, target: "es2022", minify: false, cssMinify: false },
});
