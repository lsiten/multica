import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { platform, arch } from "node:os";
const execute = promisify(execFile);
export const REMOTE_WORKER_FILES = ["vscreen-remote-worker.mjs", "vscreen-remote-route.mjs", "vscreen-remote-protocol.mjs", "vscreen-remote-identity.mjs", "vscreen-performance-browser.mjs", "vscreen-performance-metrics.mjs", "vscreen-remote-network.mjs", "../../../packages/core/runtimes/vscreen-receive-offer.mjs"];
export async function remoteWorkerHash() {
  const digest = createHash("sha256");
  for (const name of REMOTE_WORKER_FILES) { digest.update(name + "\0"); digest.update(await readFile(new URL(name, import.meta.url))); }
  return digest.digest("hex");
}
export async function machineIdentity(challenge, dependencies = {}) {
  if (!/^[a-f0-9]{64}$/.test(challenge)) throw new Error("identity_challenge_invalid");
  const os = dependencies.platform ?? platform(); let id, method;
  if (os === "darwin") { const result = await (dependencies.execute ?? execute)("/usr/sbin/ioreg", ["-rd1", "-c", "IOPlatformExpertDevice"], { timeout: 5000, maxBuffer: 65536 }); id = result.stdout.match(/"IOPlatformUUID"\s*=\s*"([A-Fa-f0-9-]{36})"/)?.[1].toLowerCase(); method = "IOPlatformUUID"; }
  else if (os === "linux") { id = (await (dependencies.readFile ?? readFile)("/etc/machine-id", "utf8")).trim().toLowerCase(); if (!/^[a-f0-9]{32}$/.test(id)) id = null; method = "etc-machine-id"; }
  else throw new Error("remote_os_unsupported");
  if (!id) throw new Error("machine_identity_unavailable");
  return { challenge, machine_id_hash: createHash("sha256").update(challenge + "\0" + id).digest("hex"), os, arch: arch(), method, assurance: "trusted-os-report-not-hardware-attestation" };
}
