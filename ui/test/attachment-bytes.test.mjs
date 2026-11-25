import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { transform } from "esbuild";
import { Resvg } from "@resvg/resvg-js";

// Isolate the real encoding function from the API's browser store. Only getUpload
// uses that import; the function under test is compiled unchanged.
const source = readFileSync(new URL("../src/lib/attachments.ts", import.meta.url), "utf8");
const { code } = await transform(source.replace('import { api } from "./api";', 'const api = {};'), { loader: "ts", format: "esm" });
const { readBase64 } = await import(`data:text/javascript;base64,${Buffer.from(code).toString("base64")}`);

function annotatedImage(x) {
  return new Resvg(`<svg xmlns="http://www.w3.org/2000/svg" width="80" height="60"><rect width="80" height="60" fill="white"/><path d="M${x} 10 L60 45" stroke="red" stroke-width="5"/></svg>`).render().asPng();
}

test("three same-name annotated PNGs encode byte-for-byte even when reads finish out of order", async () => {
  const originalReader = globalThis.FileReader;
  const reads = [];
  globalThis.FileReader = class {
    readAsDataURL(file) {
      reads.push(async () => {
        this.result = `data:${file.type};base64,${Buffer.from(await file.arrayBuffer()).toString("base64")}`;
        this.onload();
      });
    }
  };
  try {
    const originals = [annotatedImage(5), annotatedImage(25), annotatedImage(45)];
    const files = originals.map(bytes => new File([bytes], "image.png", { type: "image/png" }));
    const pending = files.map(file => readBase64(file));
    assert.equal(reads.length, 3, "all reads start immediately, one per file");
    await reads[2](); await reads[0](); await reads[1]();
    const encoded = await Promise.all(pending);
    encoded.forEach((data, i) => assert.deepEqual(Buffer.from(data, "base64"), originals[i]));
    assert.equal(new Set(encoded).size, 3, "same filenames never collapse distinct images");
  } finally {
    globalThis.FileReader = originalReader;
  }
});
