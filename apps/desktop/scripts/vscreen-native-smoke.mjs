#!/usr/bin/env node

import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { access, mkdir, readFile, realpath, stat, writeFile } from "node:fs/promises";
import { constants } from "node:fs";
import { arch, platform, release } from "node:os";
import { isAbsolute, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const execute = promisify(execFile);
const guiScenarios = ["lifecycle", "source", "video", "input", "takeover", "performance", "all"];
const scenarios = ["package", "diagnostics", ...guiScenarios];

export function parseArguments(args, env = process.env) {
  const options = { scenario: "diagnostics", allowGui: env.MULTICA_RUN_VSCREEN_GUI_SMOKE === "1" };
  for (let index = 0; index < args.length; index++) {
    const flag = args[index];
    if (flag === "--allow-gui") options.allowGui = true;
    else if (["--app", "--scenario", "--evidence", "--expect-commit"].includes(flag)) {
      const value = args[++index];
      if (!value || value.startsWith("--")) throw new Error(`Missing value for ${flag}`);
      options[{ "--app": "app", "--scenario": "scenario", "--evidence": "evidence", "--expect-commit": "expectCommit" }[flag]] = value;
    } else throw new Error(`Unknown argument: ${flag}`);
  }
  if (!options.app || !isAbsolute(options.app) || !options.app.endsWith(".app")) throw new Error("--app must name an absolute .app path");
  if (!options.evidence || !isAbsolute(options.evidence)) throw new Error("--evidence must name an absolute directory");
  if (!scenarios.includes(options.scenario)) throw new Error(`--scenario must be one of: ${scenarios.join(", ")}`);
  return options;
}

async function runCommand(command, args, options = {}) {
  try {
    const result = await execute(command, args, { encoding: "utf8", timeout: 20_000, maxBuffer: 1024 * 1024, ...options });
    return { exitCode: 0, stdout: result.stdout, stderr: result.stderr };
  } catch (error) {
    return { exitCode: typeof error.code === "number" ? error.code : 1, stdout: error.stdout ?? "", stderr: error.stderr ?? "", error: error.message };
  }
}

async function hashFile(path) {
  const digest = createHash("sha256");
  for await (const chunk of createReadStream(path)) digest.update(chunk);
  return digest.digest("hex");
}

function fail(code, message) {
  throw Object.assign(new Error(message), { code });
}

async function bundleFile(app, path) {
  const actual = await realpath(path);
  const child = relative(app, actual);
  if (!child || child === ".." || child.startsWith(`..${sep}`) || isAbsolute(child)) fail("bundle_path_escape", `File resolves outside selected app: ${path}`);
  if (!(await stat(actual)).isFile()) fail("bundle_file_missing", `Not a file: ${path}`);
  return actual;
}

function signatureKind(details) {
  if (/^Signature=adhoc\r?$/m.test(details)) return "ad-hoc";
  if (/^Authority=.+$/m.test(details)) return "certificate";
  return "unknown";
}

// Tests inject command results, but still resolve and hash their own fixture bundle.
export async function runSmoke(options, dependencies = {}) {
  const run = dependencies.runCommand ?? runCommand;
  const host = dependencies.host ?? { platform: platform(), arch: arch(), release: release() };
  const report = {
    schema_version: 1,
    started_at: new Date().toISOString(),
    scenario: options.scenario,
    status: "blocked",
    provenance: dependencies.fixture ? "test-owned-fixture" : "selected-app-bundle",
    host,
    requested_app: options.app,
    gui_exercised: false,
    notarization: "not-assessed",
    checks: [],
    limitations: ["Bundle diagnostics do not prove display lifecycle, video, input, takeover, performance, or release notarization."],
  };
  await mkdir(options.evidence, { recursive: true });
  try {
    await access(join(options.evidence, "report.json"));
    throw new Error("Evidence directory already contains report.json; choose a new directory");
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
  async function command(label, executable, args, commandOptions) {
    const result = await run(executable, args, commandOptions);
    const artifact = `${String(report.checks.length + 1).padStart(2, "0")}-${label}.json`;
    await writeFile(join(options.evidence, artifact), JSON.stringify({ executable, args, ...result }, null, 2) + "\n");
    report.checks.push({ check: label, exit_code: result.exitCode, artifact });
    return result;
  }
  async function required(label, executable, args) {
    const result = await command(label, executable, args);
    if (result.exitCode !== 0) fail(label, `${label} failed; see its command artifact`);
    return result.stdout + result.stderr;
  }
  try {
    if (host.platform !== "darwin" || !["arm64", "x64"].includes(host.arch)) fail("unsupported_host", "Native smoke requires macOS arm64 or x64");
    report.app = await realpath(options.app);
    if (!(await stat(report.app)).isDirectory()) fail("app_missing", "Selected app is not a directory");
    const plist = await bundleFile(report.app, join(report.app, "Contents", "Info.plist"));
    const info = JSON.parse(await required("bundle-plist", "/usr/bin/plutil", ["-convert", "json", "-o", "-", plist]));
    if (info.CFBundleIdentifier !== "ai.multica.desktop") fail("bundle_identifier", "Expected CFBundleIdentifier ai.multica.desktop");
    if (typeof info.CFBundleExecutable !== "string" || !/^[^/\\]+$/.test(info.CFBundleExecutable) || !info.CFBundleShortVersionString) fail("bundle_metadata", "Missing or invalid bundle executable/version");
    report.bundle = { identifier: info.CFBundleIdentifier, version: info.CFBundleShortVersionString, build: info.CFBundleVersion };
    const main = await bundleFile(report.app, join(report.app, "Contents", "MacOS", info.CFBundleExecutable));
    const helper = await bundleFile(report.app, join(report.app, "Contents", "Resources", "app.asar.unpacked", "resources", "bin", "multica"));
    await access(helper, constants.X_OK);
    report.helper = { path: helper, sha256: await hashFile(helper) };
    report.bundle.executable = main;
    report.bundle.executable_sha256 = await hashFile(main);
    const expectedArch = host.arch === "x64" ? "x86_64" : "arm64";
    for (const [label, binary] of [["desktop", main], ["helper", helper]]) {
      const architectures = (await required(`${label}-architectures`, "/usr/bin/lipo", ["-archs", binary])).trim().split(/\s+/);
      report[label === "desktop" ? "bundle" : "helper"].architectures = architectures;
      if (!architectures.includes(expectedArch)) fail("architecture_mismatch", `${label} does not contain host architecture ${expectedArch}`);
    }
    const frameworks = await required("helper-frameworks", "/usr/bin/otool", ["-L", helper]);
    for (const name of ["AppKit", "CoreGraphics", "ScreenCaptureKit", "VideoToolbox"]) {
      if (!frameworks.includes(`/${name}.framework/`)) fail("native_framework_missing", `Helper does not link ${name}`);
    }
    for (const [label, target] of [["desktop", report.app], ["helper", helper]]) {
      const details = await required(`${label}-signature`, "/usr/bin/codesign", ["-d", "--verbose=4", "-r-", target]);
      const signature = { kind: signatureKind(details), verified: false };
      report[label === "desktop" ? "bundle" : "helper"].signature = signature;
      await required(`${label}-signature-verify`, "/usr/bin/codesign", ["--verify", "--strict", ...(label === "desktop" ? ["--deep"] : []), target]);
      signature.verified = true;
      if (signature.kind === "unknown") fail("signature_unknown", `${label} signature type is unknown`);
    }
    const versionText = await required("helper-version", helper, ["--version"]);
    const version = /^multica (.+) \(commit: ([^,]+), built: ([^)]+)\)\r?\ngo: ([^,]+), os\/arch: darwin\/(arm64|amd64)\s*$/.exec(versionText);
    if (!version || ["unknown", "dev"].includes(version[2])) fail("helper_version", "Helper lacks verifiable version/commit/platform output");
    report.helper.version = version[1];
    report.helper.commit = version[2];
    report.helper.built_at = version[3];
    report.helper.go = version[4];
    if ((version[5] === "amd64" ? "x64" : version[5]) !== host.arch) fail("helper_runtime_architecture", "Helper executed under a different architecture");
    if (options.expectCommit && version[2] !== options.expectCommit) fail("commit_mismatch", "Selected helper commit differs from --expect-commit");
    if (options.scenario !== "package") {
      const probe = await command("native-diagnostics", helper, ["internal-vscreen-diagnostics"]);
      try { report.native = JSON.parse(probe.stdout); } catch { fail("probe_unavailable", "Selected helper did not return native diagnostics JSON; rebuild this app with diagnostic support"); }
      if (report.native.version !== version[1] || report.native.commit !== version[2] || report.native.os !== "darwin" || report.native.arch !== version[5]) fail("probe_identity_mismatch", "Diagnostic identity does not match the selected helper version");
      report.permission_provenance = { executable: helper, launcher: process.execPath, context: "direct-child-of-smoke-runner", desktop_launch_verified: false };
      report.limitations.push("Permission preflight belongs to this helper invocation. macOS may attribute TCC to its responsible launcher; this does not certify Desktop-launched permission state.");
      if (typeof report.native.native_supported !== "boolean" || typeof report.native.permissions?.accessibility !== "boolean" || typeof report.native.permissions?.screen_recording !== "boolean") fail("probe_invalid", "Native diagnostic response lacks capability/permission booleans");
      if (!report.native.native_supported) fail("native_unsupported", "Native virtual display selectors are unavailable");
      if (report.native.error === "permission_probe_failed") fail("permission_probe_failed", "Native permission preflight failed; permission state is unknown");
      if (!report.native.permissions.accessibility || !report.native.permissions.screen_recording) fail("permissions_missing", "Accessibility and Screen Recording must both be granted by the user; no permission was requested or reset");
      if (probe.exitCode !== 0 || report.native.error) fail("probe_failed", "Selected helper native diagnostics failed");
    }
    if (guiScenarios.includes(options.scenario)) {
      if (!options.allowGui) fail("gui_not_authorized", "GUI smoke requires explicit --allow-gui or MULTICA_RUN_VSCREEN_GUI_SMOKE=1");
      if (!["lifecycle", "source", "video"].includes(options.scenario)) fail("scenario_not_implemented", "No bundle-bound GUI harness is implemented for this scenario; no GUI was started");
      const smoke = await command("native-gui-smoke", helper, ["internal-vscreen-smoke", options.scenario, options.evidence], { timeout: 45_000, env: { ...process.env, MULTICA_RUN_VSCREEN_GUI_SMOKE: "1" } });
      try { report.gui = JSON.parse(smoke.stdout); } catch { fail("gui_result_invalid", "Bundled helper did not return a GUI result; cleanup is unconfirmed"); }
      report.gui_exercised = report.gui.gui_exercised === true;
      if (smoke.exitCode !== 0 || report.gui.status !== "passed") fail("gui_smoke_failed", "Bundled native GUI scenario failed; inspect its result and cleanup flags");
      if (report.gui.executable !== helper || report.gui.version !== version[1] || report.gui.commit !== version[2] || report.gui.scenario !== options.scenario || !report.gui_exercised || report.gui.disposed !== true || report.gui.host_closed !== true || !report.gui.display?.display_id || report.gui.source?.display_id !== report.gui.display.display_id) fail("gui_result_invalid", "GUI result lacks selected-binary identity, display/source readback, or successful cleanup");
      if (options.scenario === "video") {
        const videoPath = join(options.evidence, "virtual-screen.h264");
        if (report.gui.video?.artifact !== videoPath || report.gui.video.samples?.length !== 3 || (await readFile(videoPath)).length === 0 || await hashFile(videoPath) !== report.gui.video.sha256) fail("video_artifact_invalid", "Captured H264 artifact is missing or does not match the helper result");
      }
      report.limitations.push("Native smoke does not verify renderer playback, human handoff, input, or end-to-end latency. H264 checks cover Annex-B framing/parameter sets and timestamps, not visual decoding.");
    }
    report.status = "passed";
  } catch (error) {
    report.error = { code: error.code ?? "diagnostic_failed", message: error.message };
  }
  report.finished_at = new Date().toISOString();
  await writeFile(join(options.evidence, "report.json"), JSON.stringify(report, null, 2) + "\n");
  return report;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  try {
    const options = parseArguments(process.argv.slice(2));
    const result = await runSmoke(options);
    console.log(JSON.stringify({ status: result.status, scenario: result.scenario, report: join(options.evidence, "report.json"), error: result.error }));
    process.exitCode = result.status === "passed" ? 0 : 1;
  } catch (error) {
    console.error(error.message);
    process.exitCode = 2;
  }
}
