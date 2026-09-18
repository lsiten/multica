// @vitest-environment node

import { chmod, mkdir, mkdtemp, readFile, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { afterEach, describe, expect, it } from "vitest";
import { parseArguments, runSmoke } from "./vscreen-native-smoke.mjs";

const directories = [];
afterEach(async () => {
  await Promise.all(directories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

async function fixture(overrides = {}) {
  const directory = await realpath(await mkdtemp(join(tmpdir(), "multica-vscreen-smoke-fixture-")));
  directories.push(directory);
  const app = join(directory, "Multica.app");
  const helper = join(app, "Contents/Resources/app.asar.unpacked/resources/bin/multica");
  await mkdir(join(app, "Contents/MacOS"), { recursive: true });
  await mkdir(join(app, "Contents/Resources/app.asar.unpacked/resources/bin"), { recursive: true });
  await writeFile(join(app, "Contents/Info.plist"), "test-owned plist fixture");
  await writeFile(join(app, "Contents/MacOS/Multica"), "test-owned desktop fixture");
  await writeFile(helper, "test-owned native fixture, never executed");
  await chmod(helper, 0o755);
  const calls = [];
  const options = { app, evidence: join(directory, "evidence"), scenario: "diagnostics", allowGui: false };
  const dependencies = {
    fixture: true,
    runPerformance: async () => ({ assessment: { status: "blocked", localMeasurement: { status: "blocked" }, planCoverage: { lanAcceptance: "unverified" } }, nativeResult: { cleanup_confirmed: true } }),
    host: { platform: "darwin", arch: "arm64", release: "fixture" },
    runCommand: async (command, args, commandOptions) => {
      calls.push({ command, args, guiOptIn: commandOptions?.env?.MULTICA_RUN_VSCREEN_GUI_SMOKE });
      const replacement = await overrides.command?.(command, args);
      if (replacement) return replacement;
      let stdout = "";
      if (command.endsWith("plutil")) stdout = JSON.stringify({ CFBundleIdentifier: "ai.multica.desktop", CFBundleExecutable: "Multica", CFBundleShortVersionString: "1.2.3", CFBundleVersion: "7" });
      else if (command.endsWith("lipo")) stdout = "arm64\n";
      else if (command.endsWith("otool")) stdout = ["AppKit", "CoreGraphics", "ScreenCaptureKit", "VideoToolbox"].map((name) => `/System/Library/Frameworks/${name}.framework/${name}`).join("\n");
      else if (command.endsWith("codesign") && args[0] === "-d") stdout = "Signature=adhoc\nIdentifier=ai.multica.desktop\ndesignated => identifier ai.multica.desktop\n";
      else if (command === helper && args[0] === "--version") stdout = "multica v1.2.3-dirty (commit: abc1234, built: 2026-09-18T00:00:00Z)\ngo: go1.26.0, os/arch: darwin/arm64\n";
      else if (command === helper && args[0] === "internal-vscreen-diagnostics") stdout = JSON.stringify({ version: "v1.2.3-dirty", commit: "abc1234", os: "darwin", arch: "arm64", native_supported: true, permissions: { accessibility: true, screen_recording: true }, ...overrides.probe });
      else if (!(command.endsWith("codesign") && args[0] === "--verify")) throw new Error(`Unexpected command ${command}`);
      return { exitCode: 0, stdout, stderr: "" };
    },
  };
  return { options, dependencies, calls, helper, directory };
}

describe("bundle-bound native smoke (test-owned fixtures; no native execution)", () => {
  it("requires absolute paths and rejects unknown scenarios and arguments", () => {
    expect(() => parseArguments(["--app", "relative.app"])).toThrow("absolute .app");
    expect(() => parseArguments(["--app", "/Multica.app", "--evidence", "/tmp/e", "--scenario", "pretend"])).toThrow("--scenario");
    expect(() => parseArguments(["--unknown"])).toThrow("Unknown argument");
    expect(parseArguments(["--app", "/Multica.app", "--evidence", "/tmp/e"], {})).toMatchObject({ allowGui: false });
  });

  it("records exact selected helper identity, signature, permission context and command artifacts", async () => {
    const { options, dependencies, helper, calls } = await fixture();
    const report = await runSmoke(options, dependencies);
    expect(report).toMatchObject({ status: "passed", provenance: "test-owned-fixture", gui_exercised: false, notarization: "not-assessed", helper: { path: helper, commit: "abc1234", signature: { kind: "ad-hoc", verified: true } }, permission_provenance: { desktop_launch_verified: false } });
    expect(report.helper.sha256).toMatch(/^[a-f0-9]{64}$/);
    expect(calls.filter(({ command }) => command === helper).map(({ args }) => args)).toEqual([["--version"], ["internal-vscreen-diagnostics"]]);
    expect(JSON.parse(await readFile(join(options.evidence, "report.json"), "utf8"))).toEqual(report);
    for (const check of report.checks) expect(JSON.parse(await readFile(join(options.evidence, check.artifact), "utf8"))).toHaveProperty("exitCode", 0);
  });

  it("checks packaging without probing TCC, and never treats ad-hoc signing as notarization", async () => {
    const f = await fixture();
    const report = await runSmoke({ ...f.options, scenario: "package" }, f.dependencies);
    expect(report.status).toBe("passed");
    expect(report.native).toBeUndefined();
    expect(report.notarization).toBe("not-assessed");
    expect(f.calls.some(({ args }) => args.includes("internal-vscreen-diagnostics"))).toBe(false);
  });

  it.each([
    [{ permissions: { accessibility: false, screen_recording: true } }, "permissions_missing"],
    [{ native_supported: false }, "native_unsupported"],
    [{ permissions: {} }, "probe_invalid"],
    [{ commit: "different" }, "probe_identity_mismatch"],
  ])("fails closed for diagnostic result %j", async (probe, code) => {
    const f = await fixture({ probe });
    expect(await runSmoke(f.options, f.dependencies)).toMatchObject({ status: "blocked", error: { code } });
  });

  it.each([
    ["unsigned", (command, args) => command.endsWith("codesign") && args[0] === "-d" ? { exitCode: 1, stdout: "", stderr: "not signed" } : null, "desktop-signature"],
    ["invalid signature", (command, args) => command.endsWith("codesign") && args[0] === "--verify" ? { exitCode: 1, stdout: "", stderr: "invalid" } : null, "desktop-signature-verify"],
    ["unsupported architecture", (command) => command.endsWith("lipo") ? { exitCode: 0, stdout: "x86_64", stderr: "" } : null, "architecture_mismatch"],
    ["missing native linkage", (command) => command.endsWith("otool") ? { exitCode: 0, stdout: "libSystem", stderr: "" } : null, "native_framework_missing"],
    ["wrong bundle", (command) => command.endsWith("plutil") ? { exitCode: 0, stdout: '{"CFBundleIdentifier":"other.app"}', stderr: "" } : null, "bundle_identifier"],
    ["missing probe", (_command, args) => args[0] === "internal-vscreen-diagnostics" ? { exitCode: 1, stdout: "", stderr: "unknown command" } : null, "probe_unavailable"],
  ])("rejects %s before reporting success", async (_name, command, code) => {
    const f = await fixture({ command });
    expect(await runSmoke(f.options, f.dependencies)).toMatchObject({ status: "blocked", error: { code } });
  });

  it("does not launch any all-suite child without GUI opt-in", async () => {
    const f = await fixture();
    const result = await runSmoke({ ...f.options, scenario: "all", allowGui: false }, f.dependencies);
    expect(result.status).toBe("blocked");expect(result.children).toHaveLength(6);
    expect(result.children.every((child)=>child.error.code === "gui_not_authorized")).toBe(true);
    expect(f.calls).toHaveLength(0);
  });

  it.each(["lifecycle", "source"])("invokes the exact bundled %s harness only with explicit opt-in", async (scenario) => {
    const f = await fixture({ command: (command, args) => args[0] === "internal-vscreen-smoke" ? { exitCode: 0, stderr: "", stdout: JSON.stringify({ scenario, executable: command, version: "v1.2.3-dirty", commit: "abc1234", status: "passed", gui_exercised: true, disposed: true, host_closed: true, display: { display_id: 42 }, source: { display_id: 42 } }) } : null });
    const report = await runSmoke({ ...f.options, scenario, allowGui: true }, f.dependencies);
    expect(report).toMatchObject({ status: "passed", provenance: "test-owned-fixture", gui_exercised: true });
    expect(f.calls.at(-1)).toEqual({ command: f.helper, args: ["internal-vscreen-smoke", scenario, f.options.evidence], guiOptIn: "1" });
  });

  it("rejects a GUI harness result without successful cleanup", async () => {
    const f = await fixture({ command: (command, args) => args[0] === "internal-vscreen-smoke" ? { exitCode: 0, stderr: "", stdout: JSON.stringify({ scenario: "lifecycle", executable: command, version: "v1.2.3-dirty", commit: "abc1234", status: "passed", gui_exercised: true, disposed: false, host_closed: true, display: { display_id: 42 }, source: { display_id: 42 } }) } : null });
    expect(await runSmoke({ ...f.options, scenario: "lifecycle", allowGui: true }, f.dependencies)).toMatchObject({ status: "blocked", error: { code: "gui_result_invalid" } });
  });

  it("refuses to trust video success without a matching H264 artifact", async () => {
    const f = await fixture({ command: (command, args) => args[0] === "internal-vscreen-smoke" ? { exitCode: 0, stderr: "", stdout: JSON.stringify({ scenario: "video", executable: command, version: "v1.2.3-dirty", commit: "abc1234", status: "passed", gui_exercised: true, disposed: true, host_closed: true, display: { display_id: 42 }, source: { display_id: 42 } }) } : null });
    expect(await runSmoke({ ...f.options, scenario: "video", allowGui: true }, f.dependencies)).toMatchObject({ status: "blocked", error: { code: "video_artifact_invalid" } });
  });

  it.each(["pass", "certified PID", "old lease", "foreground", "fixture cleanup"])("checks fixture-only input evidence: %s", async (mode) => {
    let f;
    f = await fixture({ command: async (command, args) => {
      if (args[0] !== "internal-vscreen-smoke") return null;
      const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");
      const images = {};
      for (const stage of ["before", "after"]) {
        const artifact = join(f.options.evidence, `input-${stage}.png`);
        const bytes = Buffer.from(`test-owned ${stage} PNG artifact fixture`);
        await writeFile(artifact, bytes);
        images[stage] = { artifact, sha256: digest(bytes), width: 2, height: 2 };
      }
      const foreground = { pid: 7, window_id: 9, cursor_x: 11, cursor_y: 13 };
      const input = {
        scope: "test-owned-fixture-external-ax-only", fixture_bundle_id: "ai.multica.smoke." + "a".repeat(32),
        fixture_binary_sha256: digest(await readFile(f.helper)), fixture_pid: 123, fixture_window_id: 9,
        fixture_closed: mode !== "fixture cleanup", control_revoked: true, ax_press_verified: true, ax_text_verified: true,
        foreground_snapshots_unchanged: true, per_pid_certified: mode === "certified PID", ...images,
        unsupported_actions: ["key", "scroll", "drag"].map((action) => ({ action, reason: "needs_intervention", old_lease_refused: mode !== "old lease", counters_observed: true, delivered_count: 0 })),
        stages: Array.from({ length: 9 }, () => ({ foreground })),
      };
      if (mode === "foreground") input.stages[8] = { foreground: { ...foreground, pid: 8 } };
      return { exitCode: 0, stderr: "", stdout: JSON.stringify({ scenario: "input", executable: command, version: "v1.2.3-dirty", commit: "abc1234", status: "passed", gui_exercised: true, disposed: true, host_closed: true, display: { display_id: 42 }, source: { display_id: 42 }, input }) };
    } });
    const report = await runSmoke({ ...f.options, scenario: "input", allowGui: true }, f.dependencies);
    expect(report.status).toBe(mode === "pass" ? "passed" : "blocked");
    if (mode !== "pass") expect(report.error.code).toBe("input_result_invalid");
    if (mode === "pass") expect(report.limitations.some((line) => line.includes("does not certify installed user apps"))).toBe(true);
  });

  it("rejects a helper symlink outside the selected app before executing it", async () => {
    const f = await fixture();
    const external = join(f.directory, "external-helper");
    await writeFile(external, "external");
    await rm(f.helper);
    await symlink(external, f.helper);
    expect(await runSmoke(f.options, f.dependencies)).toMatchObject({ status: "blocked", error: { code: "bundle_path_escape" } });
    expect(f.calls.some(({ command }) => command === f.helper)).toBe(false);
  });

  it("records a missing app as blocked and refuses to overwrite earlier evidence", async () => {
    const f = await fixture();
    const options = { ...f.options, app: join(f.directory, "Missing.app") };
    expect(await runSmoke(options, f.dependencies)).toMatchObject({ status: "blocked", error: { code: "ENOENT" } });
    await expect(runSmoke(options, f.dependencies)).rejects.toThrow("already contains report.json");
    expect(f.calls).toEqual([]);
  });

  it("does not inspect bundles on unsupported hosts", async () => {
    const f = await fixture();
    expect(await runSmoke(f.options, { ...f.dependencies, host: { platform: "linux", arch: "x64" } })).toMatchObject({ status: "blocked", error: { code: "unsupported_host" } });
    expect(f.calls).toEqual([]);
  });
});

describe("complete takeover smoke evidence", () => {
  it.each(["pass", "single transfer", "same task", "no receipt", "old action", "no fresh observation", "wrong geometry", "no human change", "cleanup failed", "missing image", "same pixels"])("checks bundle-bound lifecycle evidence: %s", async (mode) => {
    let f;
    f = await fixture({ command: async (command, args) => {
      if (args[0] !== "internal-vscreen-smoke") return null;
      const hash = (bytes) => createHash("sha256").update(bytes).digest("hex");
      const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLttAAAAABJRU5ErkJggg==", "base64");
      const images = [];
      for (const [index, name] of ["takeover-source.png", "takeover-return.png", "takeover-continuation.png"].entries()) {
        const path = join(f.options.evidence, name); await writeFile(path, png);
        images.push({ artifact: path, sha256: hash(png), pixel_sha256: String(index + 1).repeat(64) });
      }
      const source = { display_id: 42, x: 2000, y: 0, logical_width: 1600, logical_height: 900, source: { kind: "virtual" } };
      const physical = { display_id: 1, x: 0, y: 0, logical_width: 1600, logical_height: 900, source: { kind: "physical" } };
      const takeover = {
        scope: "owned-fixture-scripted-local-owner-loopback-backend-not-db-ui", fixture_bundle_id: `ai.multica.smoke.${"a".repeat(32)}`, fixture_binary_sha256: hash(await readFile(f.helper)),
        source_task_id: "source", continuation_task_id: "continuation", intervention_id: "intervention", return_receipt_id: "actual-native-return-receipt",
        provider_stopped: true, transcript_drained: true, terminal_reported: true, stopped_ack: true, human_ack: true, return_ack: true, fresh_observe_before_input: true, old_lease_refused: true, old_action_refused: true, continuation_completed: true, cleanup_ack: true, fixture_closed: true, disposed: true, host_closed: true,
        physical_source: physical, images,
        stages: ["source-observed", "provider-stopped", "terminal-http", "awaiting_takeover-ack", "human-ack", "ready_to_continue-ack", "continuation-observed", "continuation-input"],
        placements: ["source", "human", "return", "continuation"].map((stage, index) => ({ stage, pid: 123, window_id: 9, process_start: "owned-start", display_id: index === 1 ? 1 : 42, bounds: { x: index === 1 ? 10 : 2010, y: 10, width: 640, height: 440 }, human_stage: index ? 1 : 0, text: index === 0 ? "" : index === 3 ? "Multica continuation verified" : "Multica scripted human handoff" })),
      };
      if (mode === "single transfer") takeover.stages = ["human-ack"];
      if (mode === "same task") takeover.continuation_task_id = takeover.source_task_id;
      if (mode === "no receipt") takeover.return_receipt_id = "";
      if (mode === "old action") takeover.old_action_refused = false;
      if (mode === "no fresh observation") takeover.fresh_observe_before_input = false;
      if (mode === "wrong geometry") takeover.placements[1].display_id = 42;
      if (mode === "no human change") takeover.placements[1].human_stage = 0;
      if (mode === "cleanup failed") takeover.cleanup_ack = false;
      if (mode === "missing image") takeover.images = [];
      if (mode === "same pixels") takeover.images[1].pixel_sha256 = takeover.images[0].pixel_sha256;
      return { exitCode: 0, stderr: "", stdout: JSON.stringify({ scenario: "takeover", executable: command, version: "v1.2.3-dirty", commit: "abc1234", status: "passed", gui_exercised: true, disposed: true, host_closed: true, display: { display_id: 42 }, source, takeover }) };
    } });
    const report = await runSmoke({ ...f.options, scenario: "takeover", allowGui: true }, f.dependencies);
    expect(report.status).toBe(mode === "pass" ? "passed" : "blocked");
    if (mode === "pass") expect(report.limitations.join(" ")).toContain("scripted owned-App human stage");
  });

  it("keeps incomplete performance measurement and LAN coverage blocked", async () => {
    const f = await fixture();
    expect(await runSmoke({ ...f.options, scenario: "performance", allowGui: true }, f.dependencies)).toMatchObject({ status: "blocked", error: { code: "performance_gate_failed" } });
    expect(f.calls.some((call) => call.args[0] === "internal-vscreen-smoke")).toBe(false);
  });
});

describe("explicit Desktop launcher integration",()=>{
  it("parses only the declared launcher modes",()=>{
    expect(parseArguments(["--app","/tmp/Multica.app","--evidence","/tmp/evidence","--launcher","desktop"])).toMatchObject({launcher:"desktop"});
    expect(()=>parseArguments(["--app","/tmp/Multica.app","--evidence","/tmp/evidence","--launcher","other"])).toThrow("launcher");
  });
  it("uses Desktop diagnostic provenance instead of blocking on Node TCC",async()=>{
    const f=await fixture({probe:{permissions:{accessibility:false,screen_recording:false}}});const launched=[];
    f.dependencies.launchDesktopNativeSmoke=async(options)=>{launched.push(options);return {status:"passed",reportPath:join(f.options.evidence,"desktop-result.json"),report:{desktop_launch_verified:true,cleanup_confirmed:true,native:{version:"v1.2.3-dirty",commit:"abc1234",os:"darwin",arch:"arm64",native_supported:true,permissions:{accessibility:true,screen_recording:true}}}};};
    const report=await runSmoke({...f.options,launcher:"desktop"},f.dependencies);
    expect(report.status).toBe("passed");expect(launched[0].scenario).toBe("diagnostics");expect(report.permission_provenance).toMatchObject({desktop_launch_verified:true,tcc_attribution_verified:false});
    expect(f.calls.some((call)=>call.args[0]==="internal-vscreen-diagnostics")).toBe(false);
  });
  it("maps canonical GUI names through the actual companion entry",async()=>{
    const f=await fixture();const calls=[];
    f.dependencies.launchDesktopNativeSmoke=async(options)=>{calls.push(options.scenario);return {status:"passed",reportPath:join(f.options.evidence,"desktop-result.json"),report:{desktop_launch_verified:true,cleanup_confirmed:true,native:options.scenario==="diagnostics"?{version:"v1.2.3-dirty",commit:"abc1234",os:"darwin",arch:"arm64",native_supported:true,permissions:{accessibility:true,screen_recording:true}}:{scenario:"source",executable:f.helper,version:"v1.2.3-dirty",commit:"abc1234",status:"passed",gui_exercised:true,disposed:true,host_closed:true,display:{display_id:42},source:{display_id:42}}}};};
    expect(await runSmoke({...f.options,scenario:"sources",launcher:"desktop",allowGui:true},f.dependencies)).toMatchObject({scenario:"sources",status:"passed"});expect(calls).toEqual(["diagnostics","source"]);
  });
  it("performance callback returns only bounded assessment and preserves missing LAN coverage",async()=>{
    const f=await fixture();let returned;
    f.dependencies.readPerformanceReady=async()=>({nonce:"private-never-return",base_url:"http://127.0.0.1:1"});
    f.dependencies.drivePerformanceSession=async()=>({evidence:{frames:[1,2]},assessment:{status:"blocked",localMeasurement:{status:"passed"},failed:["lan_acceptance_unverified"],planCoverage:{lanAcceptance:"unverified"}},nativeResult:{cleanup_confirmed:true}});
    f.dependencies.launchDesktopNativeSmoke=async(options)=>{
      if(options.scenario==="performance")returned=await options.onReady({directory:"/owned/private",signal:new AbortController().signal});
      return {status:options.scenario==="performance"?"blocked":"passed",cleanup_confirmed:true,reportPath:join(f.options.evidence,"desktop-result.json"),report:{desktop_launch_verified:true,cleanup_confirmed:true,native:{version:"v1.2.3-dirty",commit:"abc1234",os:"darwin",arch:"arm64",native_supported:true,permissions:{accessibility:true,screen_recording:true}}}};
    };
    const report=await runSmoke({...f.options,scenario:"performance",launcher:"desktop",allowGui:true},f.dependencies);expect(report.status).toBe("blocked");expect(returned.status).toBe("blocked");expect(JSON.stringify(returned)).not.toMatch(/nonce|base_url|frames/);expect(report.performance.assessment.localMeasurement.status).toBe("passed");
  });
});
