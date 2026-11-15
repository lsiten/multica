#!/usr/bin/env node

import { createHash } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const VERSION = "1.3.1";
const UPSTREAM_BASE =
  `https://github.com/vercel-labs/agent-desktop/releases/download/v${VERSION}`;
const LICENSE = {
  name: "agent-desktop-LICENSE",
  url: `https://raw.githubusercontent.com/vercel-labs/agent-desktop/v${VERSION}/LICENSE`,
  sha256: "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30",
  limit: 1 << 20,
};
const ASSETS = [
  ["agent-desktop-v1.3.1-aarch64-apple-darwin.tar.gz", "9b7b7cfc74892eab46c2c85d6cb7dc4863bf8dab0a222cfcfb8ae4604c69c86c"],
  ["agent-desktop-v1.3.1-x86_64-apple-darwin.tar.gz", "469da10cfb68928c58caff88b92b30526e0ca895ac06bc86b7f533b1d9b309b3"],
  ["agent-desktop-v1.3.1-aarch64-unknown-linux-gnu.tar.gz", "438990a703fbeb19775596c341c3701435e80adbfa1b2f64b82ff17567d5573b"],
  ["agent-desktop-v1.3.1-x86_64-unknown-linux-gnu.tar.gz", "ca77b6b75034eeb12e537dd8c0a4f2a3424119253f38b7e6bd79ba0d2fef1264"],
  ["agent-desktop-v1.3.1-aarch64-pc-windows-msvc.tar.gz", "805f1f61890f9983fceaf38cda94a37a049c379435d65607db5cedd22c8ef97b"],
  ["agent-desktop-v1.3.1-x86_64-pc-windows-msvc.tar.gz", "6ee4b1e078f4e78792e79f16e32e4a78b9919464aa3c55629b7e27ed53a08c13"],
].map(([name, sha256]) => ({ name, sha256, limit: 64 << 20 }));

function parseArguments(argv) {
  let output = "";
  for (let index = 0; index < argv.length; index += 1) {
    const flag = argv[index];
    if (flag !== "--output") throw new Error(`unknown argument: ${flag}`);
    const value = argv[++index];
    if (!value || value.startsWith("--")) throw new Error("--output requires a directory");
    output = value;
  }
  if (!output) throw new Error("--output is required");
  return { output };
}

export async function downloadVerified(
  fetchImpl,
  url,
  destination,
  expectedSHA256,
  limit,
) {
  const response = await fetchImpl(url, {
    redirect: "follow",
    signal: AbortSignal.timeout(120_000),
  });
  if (!response.ok) {
    throw new Error(`download ${url} returned HTTP ${response.status}`);
  }
  const declaredLength = Number(response.headers.get("content-length") ?? "0");
  if (declaredLength > limit) throw new Error(`download ${url} exceeds ${limit} bytes`);
  const bytes = Buffer.from(await response.arrayBuffer());
  if (bytes.length > limit) throw new Error(`download ${url} exceeds ${limit} bytes`);
  const actual = createHash("sha256").update(bytes).digest("hex");
  if (actual !== expectedSHA256) {
    throw new Error(`SHA-256 mismatch for ${url}: got ${actual}`);
  }
  await writeFile(destination, bytes, { flag: "wx", mode: 0o644 });
}

export async function stageReleaseAssets({
  output,
  baseURL = UPSTREAM_BASE,
  fetchImpl = fetch,
}) {
  await mkdir(output, { recursive: true });
  for (const asset of ASSETS) {
    await downloadVerified(
      fetchImpl,
      `${baseURL}/${asset.name}`,
      join(output, asset.name),
      asset.sha256,
      asset.limit,
    );
  }
  await downloadVerified(
    fetchImpl,
    LICENSE.url,
    join(output, LICENSE.name),
    LICENSE.sha256,
    LICENSE.limit,
  );
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const options = parseArguments(process.argv.slice(2));
  await stageReleaseAssets(options);
  console.log(`Staged agent-desktop v${VERSION} release assets in ${options.output}`);
}
