import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { mkdir, open, rename, rm, stat } from "node:fs/promises";
import { dirname, join } from "node:path";

interface Asset { readonly name: string; readonly size: number; readonly sha256: string }
export class VoiceDownloadError extends Error {}
async function validFile(path: string, asset: Asset): Promise<boolean> {
  try {
    if ((await stat(path)).size !== asset.size) return false;
    const hash = createHash("sha256");
    for await (const chunk of createReadStream(path)) hash.update(chunk);
    return hash.digest("hex") === asset.sha256;
  } catch (error) {
    if (error instanceof Error && "code" in error && error.code === "ENOENT") return false;
    throw error;
  }
}

export async function downloadVoiceAssets(options: {
  readonly root: string; readonly assets: readonly Asset[]; readonly baseURL: string;
  readonly fetchAsset: (url: string, options: { signal: AbortSignal }) => Promise<Response>;
  readonly signal: AbortSignal; readonly onProgress: (percent: number) => void;
}): Promise<void> {
  const total = options.assets.reduce((sum, asset) => sum + asset.size, 0);
  let complete = 0;
  for (const asset of options.assets) {
    options.signal.throwIfAborted();
    const path = join(options.root, asset.name);
    if (await validFile(path, asset)) {
      complete += asset.size; options.onProgress(Math.floor(100 * complete / total)); continue;
    }
    await mkdir(dirname(path), { recursive: true });
    const partial = path + ".partial";
    const controller = new AbortController();
    const signal = AbortSignal.any([options.signal, controller.signal]);
    let idle = setTimeout(() => controller.abort(), 30_000);
    try {
      const response = await options.fetchAsset(options.baseURL + asset.name, { signal });
      if (!response.ok || !response.body) throw new VoiceDownloadError(`Voice download HTTP ${response.status}`);
      const file = await open(partial, "w", 0o600);
      const reader = response.body.getReader();
      const hash = createHash("sha256");
      let received = 0;
      try {
        for (;;) {
          signal.throwIfAborted();
          const { value, done } = await reader.read();
          if (done) break;
          clearTimeout(idle); idle = setTimeout(() => controller.abort(), 30_000);
          received += value.byteLength;
          if (received > asset.size) throw new VoiceDownloadError("Voice asset exceeds expected size");
          hash.update(value);
          await file.writeFile(value);
          options.onProgress(Math.floor(100 * (complete + received) / total));
        }
      } finally { reader.releaseLock(); await file.close(); }
      if (received !== asset.size || hash.digest("hex") !== asset.sha256) throw new VoiceDownloadError("Voice asset checksum mismatch");
      await rename(partial, path);
      complete += asset.size;
    } finally {
      clearTimeout(idle); controller.abort();
      await rm(partial, { force: true });
    }
  }
  options.onProgress(100);
}
