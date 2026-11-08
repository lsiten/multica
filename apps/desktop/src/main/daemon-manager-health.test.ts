// @vitest-environment node
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => {
  const health: Record<string, unknown> = { status: "running" };
  return {
  root: "",
  handlers: new Map<string, (...args: unknown[]) => unknown>(),
  exec: vi.fn(),
  health,
};
});
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
vi.mock("./daemon-port", () => ({ probeProfileHealth: vi.fn(async (_profile: string, probe: (port: number) => Promise<unknown>) => probe(19515)), readDaemonPortRecord: vi.fn(async () => null) }));

async function invoke(name: string, ...args: unknown[]) {
  const handler = fixture.handlers.get(name);
  if (!handler) throw new Error(`IPC handler missing: ${name}`);
  return handler({}, ...args);
}

describe("daemon health queue metrics at renderer IPC", () => {
  beforeEach(() => {
    vi.resetModules();
    vi.useFakeTimers();
    fixture.handlers.clear();
    fixture.root = mkdtempSync(join(tmpdir(), "desktop-health-"));
    writeFileSync(join(fixture.root, "fixture-cli"), "fixture");
    fixture.exec.mockReset().mockImplementation((_bin: string, _args: string[], _options: unknown, done: (error: Error | null, stdout?: string) => void) => done(null, '{"version":"fixture"}'));
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(fixture.health))));
  });
  afterEach(() => {
    vi.clearAllTimers(); vi.useRealTimers(); vi.unstubAllGlobals();
    rmSync(fixture.root, { recursive: true });
  });
  it.each([
    { name: "current daemon", health: { status: "running", pending_jev_decision_report_count: 3, pending_jev_decision_report_bytes: 1024, failed_jev_decision_report_count: 2, failed_jev_decision_report_bytes: 512 }, expected: { pendingCount: 3, pendingBytes: 1024, failedCount: 2, failedBytes: 512 } },
    { name: "older daemon", health: { status: "running" }, expected: { pendingCount: 0, pendingBytes: 0, failedCount: 0, failedBytes: 0 } },
    { name: "malformed metrics", health: { status: "running", pending_jev_decision_report_count: -1, pending_jev_decision_report_bytes: "secret", failed_jev_decision_report_count: 1.5, failed_jev_decision_report_bytes: null }, expected: { pendingCount: 0, pendingBytes: 0, failedCount: 0, failedBytes: 0 } },
  ])("maps $name through HTTP health and daemon:get-status", async ({ health, expected }) => {
    fixture.health = health;
    const { setupDaemonManager } = await import("./daemon-manager");
    setupDaemonManager(() => null);
    await invoke("daemon:is-cli-installed");
    await invoke("daemon:set-target-api-url", "https://health.test");
    const status = await invoke("daemon:get-status");
    expect(status).toMatchObject({ state: "running", jevDecisionReports: expected });
    console.info("sanitized queue metrics delivered over daemon:get-status", expected);
  });
});
