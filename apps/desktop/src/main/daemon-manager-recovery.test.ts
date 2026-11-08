// @vitest-environment node
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({
  root: "",
  handlers: new Map<string, (...args: unknown[]) => unknown>(),
  exec: vi.fn(),
}));
vi.mock("electron", () => ({
  app: {
    getPath: () => fixture.root,
    getAppPath: () => fixture.root,
    getPreferredSystemLanguages: () => ["en"],
    on: vi.fn(),
  },
  BrowserWindow: { getAllWindows: () => [] },
  dialog: { showMessageBox: vi.fn() },
  ipcMain: {
    handle: (name: string, handler: (...args: unknown[]) => unknown) => fixture.handlers.set(name, handler),
    on: vi.fn(),
  },
}));
vi.mock("os", async (original) => ({ ...await original<typeof import("os")>(), homedir: () => fixture.root }));
vi.mock("child_process", () => ({ execFile: fixture.exec }));
vi.mock("./cli-bootstrap", () => ({
  managedCliPath: () => join(fixture.root, "fixture-cli"),
  ensureManagedCli: vi.fn(),
}));
vi.mock("./daemon-port", () => ({ probeProfileHealth: vi.fn(async () => null), readDaemonPortRecord: vi.fn(async () => null) }));

function budgetPath(host: string) {
  return join(fixture.root, ".multica", "profiles", `desktop-${host}`, "desktop_recovery_budget.json");
}
function savePausedProfile(host: string) {
  const path = budgetPath(host);
  mkdirSync(join(path, ".."), { recursive: true });
  const budget = { attemptTimestamps: Array.from({ length: 5 }, () => Date.now()), paused: true };
  writeFileSync(path, JSON.stringify(budget));
  return budget;
}
function invoke(name: string, ...args: unknown[]) {
  const handler = fixture.handlers.get(name);
  if (!handler) throw new Error(`IPC handler missing: ${name}`);
  return handler({}, ...args);
}
async function boot() {
  const { setupDaemonManager } = await import("./daemon-manager");
  setupDaemonManager(() => null);
  await invoke("daemon:is-cli-installed");
}
function starts() {
  return fixture.exec.mock.calls.filter(([, args]) => args[0] === "daemon" && args[1] === "start");
}

describe("profile recovery budget across desktop startup IPC", () => {
  beforeEach(() => {
    vi.resetModules();
    vi.useFakeTimers();
    fixture.handlers.clear();
    fixture.root = mkdtempSync(join(tmpdir(), "desktop-budget-"));
    mkdirSync(join(fixture.root, ".multica"));
    writeFileSync(join(fixture.root, "fixture-cli"), "fixture");
    writeFileSync(join(fixture.root, ".multica", "desktop_prefs.json"), JSON.stringify({ autoStart: true }));
    fixture.exec.mockReset().mockImplementation((_bin: string, args: string[], _options: unknown, done: (error: Error | null, stdout?: string) => void) => {
      done(null, args[0] === "version" ? '{"version":"fixture"}' : "");
    });
  });
  afterEach(() => {
    vi.clearAllTimers();
    vi.useRealTimers();
    rmSync(fixture.root, { recursive: true });
  });

  it.each(["daemon:start", "daemon:restart"])("retains pause through target/login and resumes only on %s", async (manualAction) => {
    const budget = savePausedProfile("one.test");
    await boot();
    await invoke("daemon:set-target-api-url", "https://one.test");
    await invoke("daemon:auto-start");
    await vi.advanceTimersByTimeAsync(30_000);
    expect(await invoke("daemon:get-status")).toMatchObject({ state: "recovery_paused" });
    expect(starts()).toHaveLength(0);
    expect(JSON.parse(readFileSync(budgetPath("one.test"), "utf8"))).toEqual(budget);

    await invoke(manualAction);
    expect(starts()).toHaveLength(1);
    expect(JSON.parse(readFileSync(budgetPath("one.test"), "utf8"))).toEqual({ attemptTimestamps: [], paused: false });
    console.info("startup -> target -> login retained pause; manual resume persisted empty profile budget", manualAction);
  });

  it("keeps a persisted pause after its timestamps expire and desktop reloads", async () => {
    savePausedProfile("one.test");
    writeFileSync(budgetPath("one.test"), JSON.stringify({ attemptTimestamps: [Date.now() - 31 * 60_000], paused: true }));
    await boot();
    await invoke("daemon:set-target-api-url", "https://one.test");
    await invoke("daemon:auto-start");
    expect(starts()).toHaveLength(0);
    vi.clearAllTimers();
    vi.resetModules();
    await boot();
    await invoke("daemon:set-target-api-url", "https://one.test");
    await invoke("daemon:auto-start");
    expect(starts()).toHaveLength(0);
    expect(await invoke("daemon:get-status")).toMatchObject({ state: "recovery_paused" });
  });

  it("spends the final automatic allowance across repeated desktop logins", async () => {
    savePausedProfile("one.test");
    writeFileSync(budgetPath("one.test"), JSON.stringify({
      attemptTimestamps: Array.from({ length: 4 }, () => Date.now() - 600_001), paused: false,
    }));
    await boot();
    await invoke("daemon:set-target-api-url", "https://one.test");
    await invoke("daemon:auto-start");
    expect(starts()).toHaveLength(1);
    await invoke("daemon:auto-start");
    expect(starts()).toHaveLength(1);

    vi.clearAllTimers();
    vi.resetModules();
    await boot();
    await invoke("daemon:set-target-api-url", "https://one.test");
    await invoke("daemon:auto-start");
    expect(starts()).toHaveLength(1);
    expect(await invoke("daemon:get-status")).toMatchObject({ state: "recovery_paused" });
    const persisted = JSON.parse(readFileSync(budgetPath("one.test"), "utf8"));
    expect(persisted.attemptTimestamps).toHaveLength(5);
    expect(persisted.paused).toBe(true);
  });

  it("keeps independent budgets when switching profiles and reloading the manager", async () => {
    const paused = savePausedProfile("one.test");
    await boot();
    await invoke("daemon:set-target-api-url", "https://one.test");
    await invoke("daemon:auto-start");
    await invoke("daemon:set-target-api-url", "https://two.test");
    await invoke("daemon:auto-start");
    expect(starts()).toHaveLength(1);
    expect(starts()[0][1]).toContain("desktop-two.test");
    await invoke("daemon:set-target-api-url", "https://one.test");
    await invoke("daemon:auto-start");
    expect(starts()).toHaveLength(1);
    expect(JSON.parse(readFileSync(budgetPath("one.test"), "utf8"))).toEqual(paused);

    vi.clearAllTimers();
    vi.resetModules();
    await boot();
    await invoke("daemon:set-target-api-url", "https://one.test");
    await invoke("daemon:auto-start");
    expect(starts()).toHaveLength(1);
    expect(await invoke("daemon:get-status")).toMatchObject({ state: "recovery_paused" });
  });
});
