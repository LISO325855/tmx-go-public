/** Encode bytes without Blob/FileReader storage; full blocks align to base64 triplets. */
export function bytesBase64(bytes: Uint8Array): string {
  const parts: string[] = [];
  const block = 24_576;
  for (let i = 0; i < bytes.length; i += block) {
    parts.push(btoa(String.fromCharCode(...bytes.subarray(i, i + block))));
  }
  return parts.join("");
}
