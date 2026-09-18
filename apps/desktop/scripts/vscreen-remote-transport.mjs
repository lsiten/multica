import { constants } from "node:fs";
import { open, mkdtemp, writeFile, rm } from "node:fs/promises";
import { isAbsolute, join } from "node:path";
import { createHash, randomBytes } from "node:crypto";
import { spawn } from "node:child_process";
import { remoteChannel } from "./vscreen-remote-protocol.mjs";
import { machineIdentity, remoteWorkerHash } from "./vscreen-remote-identity.mjs";

async function privateFile(path) {
  if (!isAbsolute(path)) throw new Error("remote_config_path_invalid");
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try { const stat = await file.stat(); if (!stat.isFile() || stat.uid !== process.getuid?.() || (stat.mode & 0o777) !== 0o600 || stat.nlink !== 1 || stat.size > 65536) throw new Error("remote_file_not_private"); return await file.readFile("utf8"); } finally { await file.close(); }
}
export async function readRemoteConfig(path) {
  const config = JSON.parse(await privateFile(path));
  const keys = ["schema_version", "host", "port", "user", "known_hosts_file", "host_key_sha256", "identity_file", "remote_node", "remote_repo", "worker_hash"];
  if (!config || Object.keys(config).some((k) => !keys.includes(k)) || config.schema_version !== 1 || !/^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$/.test(config.host) || !/^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$/.test(config.user) || !Number.isInteger(config.port) || config.port < 1 || config.port > 65535 || !/^SHA256:[A-Za-z0-9+/]{43}$/.test(config.host_key_sha256) || !/^[a-f0-9]{64}$/.test(config.worker_hash) || ![config.remote_node, config.remote_repo].every((p) => typeof p === "string" && /^\/[A-Za-z0-9_./-]+$/.test(p) && !p.split("/").includes(".."))) throw new Error("remote_config_invalid");
  const known = await privateFile(config.known_hosts_file);
  // A single explicit pinned key avoids config aliases, key discovery and fallback.
  const lines = known.trim().split(/\r?\n/); const fields = lines[0]?.split(/\s+/);
  const host = config.port === 22 ? config.host : `[${config.host}]:${config.port}`;
  if (lines.length !== 1 || fields.length !== 3 || fields[0] !== host || !["ssh-ed25519", "ecdsa-sha2-nistp256", "ssh-rsa"].includes(fields[1]) || !/^[A-Za-z0-9+/]+={0,2}$/.test(fields[2]) || "SHA256:" + createHash("sha256").update(Buffer.from(fields[2], "base64")).digest("base64").replace(/=+$/, "") !== config.host_key_sha256) throw new Error("remote_host_key_mismatch");
  await privateFile(config.identity_file);
  if (config.worker_hash !== await remoteWorkerHash()) throw new Error("remote_worker_version_mismatch");
  return { ...config, pinned_known_hosts: known };
}
export function remoteSSHArguments(config) {
  return ["-F", "/dev/null", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "GlobalKnownHostsFile=/dev/null", "-o", `UserKnownHostsFile="${config.known_hosts_file.replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`, "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=2", "-i", config.identity_file, "-p", String(config.port), "-l", config.user, "--", config.host, `/usr/bin/env -i PATH=/usr/bin:/bin LANG=C HOME="$HOME" ${config.remote_node} ${config.remote_repo}/apps/desktop/scripts/vscreen-remote-worker.mjs --stdio`];
}
export function viewerSignalGate(runID, sources, rpc) {
  const ids = new Set(["performance-0", "performance-1"]); const active = new Map(); let stopped = false;
  return { stop() { stopped = true; }, async signal(value) {
    if (stopped || value?.run_id !== runID || !ids.has(value.viewer_id)) throw new Error("remote_signal_scope");
    const { path, body } = value;
    if (path === "/clock") { if (body !== undefined && body !== null) throw new Error("remote_signal_body"); return rpc(path); }
    if (!["/offer", "/renew", "/viewer/close"].includes(path) || body?.viewer_id !== value.viewer_id) throw new Error("remote_signal_path");
    if (path === "/offer") { if (!sources.some((s) => s.source_id === body.source_id) || active.has(value.viewer_id) || body.offer?.type !== "offer" || typeof body.offer.sdp !== "string" || body.offer.sdp.length > 262144 || Object.keys(body).some((k) => !["viewer_id", "source_id", "offer"].includes(k))) throw new Error("remote_offer_scope"); active.set(value.viewer_id, body.source_id); try { return await rpc(path, body); } catch (error) { active.delete(value.viewer_id); throw error; } }
    if (Object.keys(body).some((k) => k !== "viewer_id") || !active.has(value.viewer_id)) throw new Error("remote_viewer_inactive");
    const result = await rpc(path, body); if (path === "/viewer/close") active.delete(value.viewer_id); return result;
  } };
}
export async function startRemoteViewers(ready, options, rpc, dependencies = {}) {
  if (options.allowRemoteViewer !== true || !options.remoteViewerConfig) throw new Error("remote_viewer_not_authorized");
  const config = await readRemoteConfig(options.remoteViewerConfig), runID = randomBytes(32).toString("hex"), identity = await (dependencies.machineIdentity ?? machineIdentity)(runID);
  const privateDirectory = await mkdtemp(join(options.evidence, "remote-private-"));
  const knownHosts = join(privateDirectory, "known_hosts");
  await writeFile(knownHosts, config.pinned_known_hosts, { mode: 0o600 });
  let child;
  try { child = (dependencies.spawn ?? spawn)("/usr/bin/ssh", remoteSSHArguments({ ...config, known_hosts_file: knownHosts }), { env: { PATH: "/usr/bin:/bin", LANG: "C" }, stdio: ["pipe", "pipe", "pipe"] }); } catch { await rm(privateDirectory, { recursive: true, force: true }); throw new Error("remote_process_launch_failed"); }
  let exited = false, code = null, stderrBytes = 0;
  const exit = new Promise((resolve) => { child.once("error", () => { exited = true; resolve(); }); child.once("close", (value) => { exited = true; code = value; resolve(); }); });
  child.stderr.on("data", (value) => { stderrBytes += value.length; if (stderrBytes > 65536) child.kill("SIGTERM"); });
  const gate = viewerSignalGate(runID, ready.sources, rpc), channel = remoteChannel(child.stdout, child.stdin, { signal: gate.signal });
  const abort = () => { gate.stop(); channel.close(); child.stdin.end(); child.kill("SIGTERM"); };
  options.signal?.addEventListener("abort", abort, { once: true });
  const close = async () => {
    let confirmed = false; try { confirmed = (await channel.request("close", { run_id: runID })).cleanup_confirmed === true; } catch { confirmed = false; } finally { gate.stop(); channel.close(); child.stdin.end(); options.signal?.removeEventListener("abort", abort); }
    await Promise.race([exit, new Promise((resolve) => { const timer = setTimeout(resolve, 5000); exit.then(() => clearTimeout(timer)); })]);
    await rm(privateDirectory, { recursive: true, force: true });
    if (!exited) { child.kill("SIGTERM"); throw new Error("remote_exit_unconfirmed"); }
    if (!confirmed || code !== 0) throw new Error("remote_cleanup_unconfirmed");
  };
  try {
    if (options.signal?.aborted) throw new Error("remote_aborted");
    const hello = await channel.request("hello", { run_id: runID, worker_hash: config.worker_hash });
    if (hello.run_id !== runID || hello.worker_hash !== config.worker_hash || hello.identity?.challenge !== runID || !/^[a-f0-9]{64}$/.test(hello.identity.machine_id_hash) || hello.identity.machine_id_hash === identity.machine_id_hash) throw new Error("remote_machine_not_independent");
    const started = await channel.request("start", { run_id: runID, sources: ready.sources.map(({ source_id, source_tag }) => ({ source_id, source_tag })) }, 30000);
    if (started.fresh_contexts !== 2 || JSON.stringify(started.viewer_ids) !== JSON.stringify(["performance-0", "performance-1"])) throw new Error("remote_context_identity_invalid");
    const peers = started.viewer_ids.map((viewerID) => ({ sample: () => channel.request("sample", { run_id: runID, viewer_id: viewerID }), switchSource: (source, phase) => channel.request("switch", { run_id: runID, viewer_id: viewerID, source_id: source.source_id, phase }), close: async () => {} }));
    return { peers, close, evidence: { kind: "controlled-ssh-worker", run_id: runID, host_identity: identity, remote_identity: hello.identity, worker_hash: hello.worker_hash, local_worker_hash: config.worker_hash, ssh_host_key: config.host_key_sha256, browser_version: started.browser_version, fresh_contexts: 2, sources: ready.sources.map(({source_id,source_tag})=>({source_id,source_tag})), observations: [], switch_observations: [] } };
  } catch (error) { try { await close(); } catch { abort(); } throw error; }
}
