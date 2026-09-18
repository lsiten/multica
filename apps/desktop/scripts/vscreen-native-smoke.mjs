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
      if (!["lifecycle", "source", "video", "input", "takeover"].includes(options.scenario)) fail("scenario_not_implemented", "No bundle-bound GUI harness is implemented for this scenario; no GUI was started");
      const smoke = await command("native-gui-smoke", helper, ["internal-vscreen-smoke", options.scenario, options.evidence], { timeout: options.scenario === "takeover" ? 120_000 : options.scenario === "input" ? 90_000 : 45_000, env: { ...process.env, MULTICA_RUN_VSCREEN_GUI_SMOKE: "1" } });
      try { report.gui = JSON.parse(smoke.stdout); } catch { fail("gui_result_invalid", "Bundled helper did not return a GUI result; cleanup is unconfirmed"); }
      report.gui_exercised = report.gui.gui_exercised === true;
      if (smoke.exitCode !== 0 || report.gui.status !== "passed") fail("gui_smoke_failed", "Bundled native GUI scenario failed; inspect its result and cleanup flags");
      if (report.gui.executable !== helper || report.gui.version !== version[1] || report.gui.commit !== version[2] || report.gui.scenario !== options.scenario || !report.gui_exercised || report.gui.disposed !== true || report.gui.host_closed !== true || !report.gui.display?.display_id || report.gui.source?.display_id !== report.gui.display.display_id) fail("gui_result_invalid", "GUI result lacks selected-binary identity, display/source readback, or successful cleanup");
      if (options.scenario === "video") {
        const videoPath = join(options.evidence, "virtual-screen.h264");
        if (report.gui.video?.artifact !== videoPath || report.gui.video.samples?.length !== 3 || (await readFile(videoPath)).length === 0 || await hashFile(videoPath) !== report.gui.video.sha256) fail("video_artifact_invalid", "Captured H264 artifact is missing or does not match the helper result");
      }
      if (options.scenario === "takeover") {
        await verifyTakeoverEvidence(report.gui, options.evidence, report.helper.sha256);
        report.limitations.push("Takeover uses a same-binary owned provider, a loopback fixture backend and scripted owned-App human stage. It does not prove real model, real DB, Desktop UI/manual human acceptance, installed user apps, TCC attribution, or performance.");
      }
      if (options.scenario === "input") {
        const input = report.gui.input;
        if (input?.scope !== "test-owned-fixture-external-ax-only" || input.fixture_binary_sha256 !== report.helper.sha256 || !/^ai\.multica\.smoke\.[a-f0-9]{32}$/.test(input.fixture_bundle_id ?? "") || !Number.isInteger(input.fixture_pid) || input.fixture_pid <= 0 || !Number.isInteger(input.fixture_window_id) || input.fixture_window_id <= 0 || input.fixture_closed !== true || input.control_revoked !== true || input.ax_press_verified !== true || input.ax_text_verified !== true || input.foreground_snapshots_unchanged !== true || input.per_pid_certified !== false) fail("input_result_invalid", "Input result lacks verified fixture identity, semantic readback, isolation, or cleanup");
        if (input.unsupported_actions?.length !== 3 || ["key", "scroll", "drag"].some((action, index) => input.unsupported_actions[index]?.action !== action || input.unsupported_actions[index]?.reason !== "needs_intervention" || input.unsupported_actions[index]?.old_lease_refused !== true || input.unsupported_actions[index]?.counters_observed !== true || input.unsupported_actions[index]?.delivered_count !== 0)) fail("input_result_invalid", "Uncertified PID input must be refused with no delivered events and an expired old lease");
        const baseline = input.stages?.[0]?.foreground;
        if (input.stages?.length !== 9 || !baseline?.pid || !baseline?.window_id || input.stages.some((stage) => JSON.stringify(stage.foreground) !== JSON.stringify(baseline))) fail("input_result_invalid", "Foreground and cursor isolation snapshots are incomplete");
        for (const [stage, name] of [["before", "input-before.png"], ["after", "input-after.png"]]) {
          const image = input[stage]; const path = join(options.evidence, name);
          if (image?.artifact !== path || !(image.width > 0 && image.height > 0) || (await readFile(path)).length === 0 || await hashFile(path) !== image.sha256) fail("input_artifact_invalid", "Fixture PNG evidence is missing or has changed");
        }
        if (input.before.sha256 === input.after.sha256) fail("input_artifact_invalid", "Fixture pixels did not change");
        report.limitations.push("Input proves only this copied-helper test fixture's external AX press/value behavior and refusal of uncertified PID input. It does not certify installed user apps, per-PID input support, IME, continuous foreground typing, or final Desktop TCC attribution.");
      } else if (options.scenario !== "takeover") {
        report.limitations.push("Native smoke does not verify renderer playback, human handoff, input, or end-to-end latency. H264 checks cover Annex-B framing/parameter sets and timestamps, not visual decoding.");
      }
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


export async function verifyTakeoverEvidence(gui, directory, helperHash) {
  const takeover = gui.takeover;
  if (takeover?.scope !== "owned-fixture-scripted-local-owner-loopback-backend-not-db-ui" || takeover.fixture_binary_sha256 !== helperHash || !/^ai\.multica\.smoke\.[a-f0-9]{32}$/.test(takeover.fixture_bundle_id ?? "")) fail("takeover_result_invalid", "Takeover fixture/bundle provenance is missing");
  for (const key of ["provider_stopped", "transcript_drained", "terminal_reported", "stopped_ack", "human_ack", "return_ack", "fresh_observe_before_input", "old_lease_refused", "old_action_refused", "continuation_completed", "cleanup_ack", "fixture_closed", "disposed", "host_closed"]) {
    if (takeover[key] !== true) fail("takeover_result_invalid", `Takeover missing confirmed ${key}`);
  }
  if (!takeover.source_task_id || !takeover.continuation_task_id || takeover.source_task_id === takeover.continuation_task_id || !takeover.intervention_id || !takeover.return_receipt_id) fail("takeover_result_invalid", "Distinct continuation and native return receipt are required");
  const expectedStages = ["source-observed", "provider-stopped", "terminal-http", "awaiting_takeover-ack", "human-ack", "ready_to_continue-ack", "continuation-observed", "continuation-input"];
  if (JSON.stringify(takeover.stages) !== JSON.stringify(expectedStages)) fail("takeover_result_invalid", "Takeover lifecycle ordering is incomplete");
  const phases = ["source", "human", "return", "continuation"];
  if (takeover.placements?.length !== phases.length || takeover.physical_source?.source?.kind !== "physical" || takeover.physical_source.display_id === gui.display.display_id) fail("takeover_result_invalid", "Physical/virtual placement evidence is missing");
  const first = takeover.placements[0];
  if (!Number.isInteger(first.pid) || first.pid <= 0 || !Number.isInteger(first.window_id) || first.window_id <= 0 || !first.process_start) fail("takeover_result_invalid", "Owned fixture process/window identity is missing");
  for (const [index, place] of takeover.placements.entries()) {
    const source = index === 1 ? takeover.physical_source : gui.source;
    const b = place.bounds;
    if (place.stage !== phases[index] || place.pid !== first.pid || place.window_id !== first.window_id || place.process_start !== first.process_start || place.display_id !== source.display_id || !b || ![b.x,b.y,b.width,b.height,source.x,source.y,source.logical_width,source.logical_height].every(Number.isFinite) || b.width <= 0 || b.height <= 0 || b.x < source.x || b.y < source.y || b.x+b.width > source.x+source.logical_width || b.y+b.height > source.y+source.logical_height || place.human_stage !== (index === 0 ? 0 : 1)) fail("takeover_result_invalid", "Placement is not verified by the owned window readback");
  }
  if (takeover.placements[1].text !== "Multica scripted human handoff" || takeover.placements[2].text !== "Multica scripted human handoff" || takeover.placements[3].text !== "Multica continuation verified") fail("takeover_result_invalid", "Human/continuation changes were not observed");
  const names = ["takeover-source.png", "takeover-return.png", "takeover-continuation.png"];
  if (takeover.images?.length !== names.length) fail("takeover_artifact_invalid", "Native PNG observations are missing");
  for (const [index, name] of names.entries()) {
    const image = takeover.images[index]; const path = join(directory, name); const raw = await readFile(path);
    if (image.artifact !== path || raw.length < 24 || !raw.subarray(0,8).equals(Buffer.from([137,80,78,71,13,10,26,10])) || !raw.readUInt32BE(16) || !raw.readUInt32BE(20) || await hashFile(path) !== image.sha256 || !/^[a-f0-9]{64}$/.test(image.pixel_sha256 ?? "")) fail("takeover_artifact_invalid", "Native PNG observation changed or is invalid");
  }
  if (takeover.images[0].pixel_sha256 === takeover.images[1].pixel_sha256) fail("takeover_artifact_invalid", "Human stage pixels did not change");
}
