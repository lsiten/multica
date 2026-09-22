// @vitest-environment node
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, it, vi } from "vitest";
import { downloadVoiceAssets } from "./voice-download";
const roots: string[] = [];
afterEach(async () => { await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))); });
async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "multica-voice-")); roots.push(root);
  const bytes = Buffer.from("verified model");
  const asset = { name: "model.bin", size: bytes.length, sha256: createHash("sha256").update(bytes).digest("hex") };
  return { root, bytes, asset };
}
it("downloads and verifies files before reporting completion, then works offline", async () => {
  const { root, bytes, asset } = await fixture();
  const fetchAsset = vi.fn(async () => new Response(bytes)); const progress: number[] = [];
  await downloadVoiceAssets({ root, assets: [asset], baseURL: "https://fixture.invalid/", fetchAsset, signal: new AbortController().signal, onProgress: (value) => progress.push(value) });
  expect(await readFile(join(root, asset.name))).toEqual(bytes);
  expect(progress.at(-1)).toBe(100);
  await downloadVoiceAssets({ root, assets: [asset], baseURL: "https://fixture.invalid/", fetchAsset, signal: new AbortController().signal, onProgress: () => {} });
  expect(fetchAsset).toHaveBeenCalledTimes(1);
});
it("rejects corrupted downloads without promoting them into the cache", async () => {
  const { root, asset } = await fixture();
  await expect(downloadVoiceAssets({ root, assets: [asset], baseURL: "https://fixture.invalid/", fetchAsset: async () => new Response("broken"), signal: new AbortController().signal, onProgress: () => {} })).rejects.toThrow();
  await expect(readFile(join(root, asset.name))).rejects.toThrow();
});
it("repairs a corrupted cache file on retry", async () => {
  const { root, bytes, asset } = await fixture(); await writeFile(join(root, asset.name), "corrupt");
  await downloadVoiceAssets({ root, assets: [asset], baseURL: "https://fixture.invalid/", fetchAsset: async () => new Response(bytes), signal: new AbortController().signal, onProgress: () => {} });
  expect(await readFile(join(root, asset.name))).toEqual(bytes);
});
