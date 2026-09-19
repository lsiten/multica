import { createHash } from "node:crypto";
import { createReadStream, constants, existsSync, lstatSync, realpathSync, openSync, fstatSync, readFileSync, closeSync, mkdirSync, writeFileSync } from "node:fs";
import { lstat, realpath, writeFile } from "node:fs/promises";
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { spawn } from "node:child_process";
import { Socket } from "node:net";

const scenarios = ["diagnostics", "lifecycle", "source", "video", "input", "takeover", "performance", "input-qualification"] as const;
type Scenario = typeof scenarios[number];
interface Invocation {
  schema: 1; nonce: string; parentPID: number; app: string; mainSHA256: string; entrySHA256: string;
  helper: { version: string; commit: string; sha256: string };
  scenario: Scenario; interactive?: boolean; allowGui: boolean; expiresAt: number; timeoutMs: number;
}
interface SmokeApp {
  isPackaged: boolean;
  setName(name: string): void;
  setPath(name: "userData" | "sessionData" | "crashDumps", path: string): void;
  setAppLogsPath(path: string): void;
  commandLine: { appendSwitch(name: string, value?: string): void };
  whenReady(): Promise<void>;
}
interface CommandResult {
  code: number | null; pid?: number; stdout: string; stderr: string;
  closed: boolean; forced: boolean; aborted: boolean;
}
interface Dependencies {
  entryPath: string; executable: string; platform: string; pid: number; parentPID: number; uid: number;
  guiOptIn: boolean;
  watchParent: (onClose: () => void) => () => void;
  run: (file: string, args: string[], timeout: number, signal: AbortSignal, gui: boolean) => Promise<CommandResult>;
}

function errorCode(error: unknown): string {
  if (error instanceof Error && /^[a-z_]{1,64}$/.test(error.message)) return error.message;
  return "desktop_smoke_failed";
}
async function hashFile(path: string): Promise<string> {
  const digest = createHash("sha256");
  for await (const bytes of createReadStream(path)) digest.update(bytes);
  return digest.digest("hex");
}
async function contained(app: string, path: string): Promise<string> {
  const actual = await realpath(path); const child = relative(app, actual);
  if (!child || child === ".." || child.startsWith(`..${sep}`) || isAbsolute(child) || !(await lstat(actual)).isFile()) throw new Error("bundle_path_escape");
  return actual;
}

async function containedBundledHelper(app: string): Promise<string> {
  const resources = join(app, "Contents", "Resources", "app.asar.unpacked", "resources");
  const daemonHelper = join(resources, "MulticaDaemon.app", "Contents", "MacOS", "multica");
  if (existsSync(daemonHelper)) return contained(app, daemonHelper);
  return contained(app, join(resources, "bin", "multica"));
}
function parseInvocation(raw: string): Invocation {
  const value: unknown = JSON.parse(raw);
  if (!value || typeof value !== "object") throw new Error("invalid_invocation");
  const v = value as Partial<Invocation>;
  if (v.schema !== 1 || typeof v.nonce !== "string" || !/^[a-f0-9]{64}$/.test(v.nonce) || !Number.isSafeInteger(v.parentPID) || !v.parentPID || typeof v.app !== "string" || !isAbsolute(v.app) || !v.app.endsWith(".app") || !/^[a-f0-9]{64}$/.test(v.mainSHA256 ?? "") || !/^[a-f0-9]{64}$/.test(v.entrySHA256 ?? "") || !v.helper || !/^[a-f0-9]{64}$/.test(v.helper.sha256) || typeof v.helper.version !== "string" || typeof v.helper.commit !== "string" || !/^[a-zA-Z0-9.-]{1,128}$/.test(v.helper.commit) || !scenarios.includes(v.scenario as Scenario) || typeof v.allowGui !== "boolean" || typeof v.expiresAt !== "number" || v.expiresAt < Date.now() || v.expiresAt > Date.now() + 600_000 || typeof v.timeoutMs !== "number" || v.timeoutMs < 1000 || v.timeoutMs > (v.scenario === "performance" ? 2_700_000 : 180_000)) throw new Error("invalid_invocation");
  if (v.interactive !== undefined && (typeof v.interactive !== "boolean" || (v.interactive && v.scenario !== "input-qualification"))) throw new Error("invalid_invocation");
  return v as Invocation;
}

/** Launch only an owned child, with bounded output and close acknowledgement. No product env is inherited. */
export function runSmokeChild(file: string, args: string[], timeout: number, signal: AbortSignal, gui: boolean): Promise<CommandResult> {
  return new Promise((resolveResult) => {
    const child = spawn(file, args, { env: { PATH: "/usr/bin:/bin:/usr/sbin:/sbin", LANG: "en_US.UTF-8", ...(gui ? { MULTICA_RUN_VSCREEN_GUI_SMOKE: "1" } : {}) }, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "", stderr = "", size = 0, forced = false, aborted = false, settled = false;
    let killTimer: ReturnType<typeof setTimeout> | undefined;
    let reapTimer: ReturnType<typeof setTimeout> | undefined;
    const finish = (code: number | null, closed: boolean) => {
      if (settled) return; settled = true;
      clearTimeout(timer); clearTimeout(killTimer); clearTimeout(reapTimer); signal.removeEventListener("abort", stop);
      resolveResult({ code, pid: child.pid, stdout, stderr, closed, forced, aborted });
    };
    const stop = () => {
      if (aborted || settled) return; aborted = true;
      child.kill("SIGTERM");
      killTimer = setTimeout(() => { if (!settled) { forced = true; child.kill("SIGKILL"); reapTimer = setTimeout(() => finish(null, false), 3000); } }, 25_000);
    };
    const timer = setTimeout(stop, timeout);
    signal.addEventListener("abort", stop, { once: true });
    const receive = (kind: "stdout" | "stderr", bytes: Buffer) => {
      size += bytes.length;
      if (size > 1024 * 1024) { stop(); return; }
      if (kind === "stdout") stdout += bytes.toString("utf8"); else stderr += bytes.toString("utf8");
    };
    child.stdout.on("data", (bytes: Buffer) => receive("stdout", bytes));
    child.stderr.on("data", (bytes: Buffer) => receive("stderr", bytes));
    child.once("error", () => finish(null, false));
    child.once("close", (code) => finish(code, true));
    if (signal.aborted) stop();
  });
}

/** Smoke mode never imports normal startup, touches accounts, acquires the user app lock, or creates a renderer. */
export async function runDesktopNativeSmoke(app: SmokeApp, path: string | undefined, overrides: Partial<Dependencies> = {}): Promise<number> {
  const deps: Dependencies = { entryPath: "", executable: process.execPath, platform: process.platform, pid: process.pid, parentPID: process.ppid, uid: process.getuid?.() ?? -1, guiOptIn: process.env.MULTICA_RUN_VSCREEN_GUI_SMOKE === "1", watchParent: watchParentPipe, run: runSmokeChild, ...overrides };
  const controller = new AbortController();
  const onSignal = () => controller.abort();
  process.once("SIGTERM", onSignal); process.once("SIGINT", onSignal);
  let reportPath: string | undefined;
  let stopParentWatch: (() => void) | undefined;
  let deadline: ReturnType<typeof setTimeout> | undefined;
  const report: Record<string, unknown> = { schema: 1, status: "blocked", launcher: "packaged-desktop-main", desktop_launch_verified: false, tcc_attribution_verified: false, cleanup_confirmed: false, commands: [] };
  try {
    if (deps.platform !== "darwin" || !app.isPackaged) throw new Error("packaged_macos_app_required");
    if (!path || !isAbsolute(path) || basename(path) !== "invocation.json") throw new Error("invalid_invocation_path");
    const directory = dirname(path); const parent = lstatSync(directory);
    if (!parent.isDirectory() || parent.isSymbolicLink() || parent.uid !== deps.uid || (parent.mode & 0o077) !== 0 || realpathSync(directory) !== resolve(directory)) throw new Error("unsafe_invocation_directory");
    const file = openSync(path, constants.O_RDONLY | constants.O_NOFOLLOW);
    let raw: string;
    try { const stat = fstatSync(file); if (!stat.isFile() || stat.uid !== deps.uid || (stat.mode & 0o777) !== 0o600 || stat.nlink !== 1 || stat.size > 8192) throw new Error("unsafe_invocation_file"); raw = readFileSync(file,"utf8"); } finally { closeSync(file); }
    const cfg = parseInvocation(raw);
    if (cfg.parentPID !== deps.parentPID) throw new Error("foreign_launcher");
    const selectedApp = realpathSync(cfg.app);
    const executable = realpathSync(deps.executable);
    if (dirname(executable) !== join(selectedApp, "Contents", "MacOS")) throw new Error("desktop_identity_mismatch");
    if (deps.entryPath!==join(selectedApp,"Contents/Resources/app.asar/out/main/index.js") || createHash("sha256").update(readFileSync(deps.entryPath)).digest("hex")!==cfg.entrySHA256) throw new Error("bootstrap_identity_mismatch");
    if (cfg.scenario !== "diagnostics" && (!cfg.allowGui || !deps.guiOptIn)) throw new Error("gui_not_authorized");
    const invocationHash = createHash("sha256").update(raw).digest("hex");
    const used = openSync(join(directory,"consumed"),"wx",0o600); try {writeFileSync(used,invocationHash);} finally {closeSync(used);}
    reportPath = join(directory, "desktop-native-smoke.json");
    stopParentWatch = deps.watchParent(() => controller.abort());
    report.invocation_sha256 = invocationHash;
    const userData = join(directory, "user-data");
    for (const name of ["", "session", "crashes", "logs"]) {
      const target=join(userData,name);
      try {mkdirSync(target,{mode:0o700});} catch(error) {if (!(error instanceof Error) || !("code" in error) || error.code!=="EEXIST") throw error;}
      const entry=lstatSync(target);if (!entry.isDirectory() || entry.isSymbolicLink() || entry.uid!==deps.uid || (entry.mode & 0o077)!==0) throw new Error("unsafe_user_data");
    }
    app.setPath("userData", userData); app.setPath("sessionData", join(userData, "session")); app.setPath("crashDumps", join(userData, "crashes")); app.setAppLogsPath(join(userData, "logs"));
    for (const flag of ["disable-background-networking", "disable-component-update", "disable-sync", "no-first-run"]) app.commandLine.appendSwitch(flag);
    if (await hashFile(executable)!==cfg.mainSHA256) throw new Error("desktop_identity_mismatch");
    deadline = setTimeout(() => controller.abort(), cfg.timeoutMs + 45_000);
    if (controller.signal.aborted) throw new Error("desktop_smoke_cancelled");
    await Promise.race([app.whenReady(), new Promise<never>((_, reject) => controller.signal.addEventListener("abort", () => reject(new Error("desktop_smoke_cancelled")), { once: true }))]);
    const helper = await containedBundledHelper(selectedApp);
    if (await hashFile(helper) !== cfg.helper.sha256) throw new Error("helper_identity_mismatch");
    const commands = report.commands as Array<Record<string, unknown>>;
    async function run(label: string, binary: string, args: string[], gui = false, allowFailure = false) {
      if (controller.signal.aborted) throw new Error("desktop_smoke_cancelled");
      const result = await deps.run(binary, args, gui ? cfg.timeoutMs : 10_000, controller.signal, gui);
      commands.push({ label, executable: binary, pid: result.pid, exit_code: result.code, closed: result.closed, forced: result.forced, aborted: result.aborted });
      if (!result.closed || result.forced || result.aborted) throw new Error("child_cleanup_unconfirmed");
      if (result.code !== 0 && !allowFailure) throw new Error("child_failed");
      return result;
    }
    const plist = await run("bundle-identity", "/usr/bin/plutil", ["-convert", "json", "-o", "-", join(selectedApp, "Contents", "Info.plist")]);
    const metadata = JSON.parse(plist.stdout) as Record<string, unknown>;
    if (metadata.CFBundleIdentifier !== "ai.multica.desktop" || metadata.CFBundleExecutable !== basename(executable)) throw new Error("bundle_identity_mismatch");
    await run("app-signature", "/usr/bin/codesign", ["--verify", "--strict", "--deep", selectedApp]);
    await run("helper-signature", "/usr/bin/codesign", ["--verify", "--strict", helper]);
    const identity = await run("helper-version", helper, ["--version"]);
    if (!identity.stdout.startsWith(`multica ${cfg.helper.version} (commit: ${cfg.helper.commit},`)) throw new Error("helper_build_mismatch");
    if (await hashFile(helper) !== cfg.helper.sha256) throw new Error("helper_identity_mismatch");
    report.parent = { entry_sha256: cfg.entrySHA256, platform: deps.platform, arch: process.arch, code_signature_verified: true, pid: deps.pid, parent_pid: deps.parentPID, executable, sha256: cfg.mainSHA256, bundle_id: metadata.CFBundleIdentifier, bundle_version: metadata.CFBundleShortVersionString };
    report.helper = { executable: helper, ...cfg.helper };
    report.scenario = cfg.scenario;
    const result = await run("native-scenario", helper, cfg.scenario === "diagnostics" ? ["internal-vscreen-diagnostics"] : cfg.scenario === "input-qualification" ? ["internal-vscreen-input-qualification", directory, ...(cfg.interactive ? ["--interactive"] : [])] : ["internal-vscreen-smoke", cfg.scenario, directory], cfg.scenario !== "diagnostics", true);
    let native: unknown;
    if (cfg.scenario === "performance") {
      const lines = result.stdout.trim().split("\n").map((line) => JSON.parse(line) as Record<string, unknown>);
      const results = lines.filter((line) => line.type === "performance-result");
      if (results.length !== 1 || lines.at(-1) !== results[0] || results[0]?.schema_version !== 1) throw new Error("native_result_mismatch");
      const readyLines = lines.filter((line) => line.type === "performance-ready");
      const ready = readyLines[0];
      if (readyLines.length !== 1 || lines[0] !== ready || ready.schema_version !== 1 || ready.executable !== helper || ready.version !== cfg.helper.version || ready.commit !== cfg.helper.commit || "nonce" in ready) throw new Error("native_result_mismatch");
      native = { ...results[0], version: ready.version, commit: ready.commit, executable: helper, scenario: cfg.scenario };
    } else {
      native = JSON.parse(result.stdout);
      if (cfg.scenario === "input-qualification") {
        const qualification = native as Record<string, unknown>;
        report.qualification = qualification;
        if (!qualification || qualification.scope !== "experimental-same-bundle-disposable-fixture" || qualification.production_certified !== false || typeof qualification.foreground_continuity !== "string" || !qualification.manual || typeof qualification.manual !== "object") throw new Error("native_result_mismatch");
        native = qualification.native;
      }
    }
    if (!native || typeof native !== "object" || !("version" in native) || native.version !== cfg.helper.version || !("commit" in native) || native.commit !== cfg.helper.commit) throw new Error("native_result_mismatch");
    if (cfg.scenario!=="diagnostics" && (!("executable" in native) || native.executable!==helper || !("scenario" in native) || native.scenario!==cfg.scenario)) throw new Error("native_result_mismatch");
    if (cfg.scenario=== "diagnostics" && (!("permissions" in native) || !native.permissions || typeof native.permissions!=="object" || !("accessibility" in native.permissions) || typeof native.permissions.accessibility!=="boolean" || !("screen_recording" in native.permissions) || typeof native.permissions.screen_recording!=="boolean")) throw new Error("native_result_mismatch");
    report.helper = { executable: helper, ...cfg.helper, child_pid: result.pid, code_signature_verified: true };
    report.native = native;
    report.desktop_launch_verified = true;
    report.permission_context = { query_process: "verified_bundle_helper_child", responsible_tcc_identity: "not_inferred_from_process_parentage" };
    if (result.code !== 0) throw new Error("native_scenario_failed");
    if (cfg.scenario === "performance" && (!("cleanup_confirmed" in native) || native.cleanup_confirmed !== true || !("errors" in native) || !Array.isArray(native.errors) || native.errors.length !== 0)) throw new Error("native_cleanup_unconfirmed");
    if (cfg.scenario !== "diagnostics" && cfg.scenario !== "performance" && (!("status" in native) || native.status !== "passed" || !("disposed" in native) || native.disposed !== true || !("host_closed" in native) || native.host_closed !== true)) throw new Error("native_cleanup_unconfirmed");
    if (cfg.scenario === "input-qualification") {
      const qualification = report.qualification as Record<string, unknown>;
      const manual = qualification.manual as Record<string, unknown>;
      if (cfg.interactive && (qualification.foreground_continuity !== "verified_manual_fixture_challenge" || manual.status !== "verified_manual_fixture_challenge" || manual.user_confirmed !== true || manual.scratch_closed !== true)) throw new Error("qualification_manual_incomplete");
      if (qualification.effects_verified !== true || qualification.completion_verified !== true || qualification.fixture_closed !== true || qualification.control_revoked !== true || qualification.old_lease_refused !== true) throw new Error("qualification_incomplete");
    }
    report.cleanup_confirmed = true;
    report.status = "passed";
  } catch (error) { report.error = errorCode(error); }
  finally {
    stopParentWatch?.(); clearTimeout(deadline); process.removeListener("SIGTERM", onSignal); process.removeListener("SIGINT", onSignal);
    if (reportPath) await writeFile(reportPath, JSON.stringify(report, null, 2) + "\n", { flag: "wx", mode: 0o600 });
  }
  return report.status === "passed" ? 0 : 1;
}

function watchParentPipe(onClose: () => void): () => void {
  const stream = new Socket({ fd: 3, readable: true, writable: false });
  stream.on("end", onClose); stream.on("error", onClose); stream.resume();
  return () => { stream.removeListener("end", onClose); stream.removeListener("error", onClose); stream.on("error", () => undefined); stream.destroy(); };
}
