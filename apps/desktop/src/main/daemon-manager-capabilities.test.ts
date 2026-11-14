// @vitest-environment node
import { mkdtempSync, rmSync, writeFileSync, mkdirSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({ root: "", handlers: new Map<string, (...args: unknown[]) => unknown>(), exec: vi.fn() }));
vi.mock("electron", () => ({
  app: { getPath: () => fixture.root, getAppPath: () => fixture.root, getPreferredSystemLanguages: () => ["en"], on: vi.fn() },
  BrowserWindow: { getAllWindows: () => [] },
  dialog: { showMessageBox: vi.fn() },
  ipcMain: { handle: (name: string, handler: (...args: unknown[]) => unknown) => fixture.handlers.set(name, handler), on: vi.fn() },
}));
vi.mock("os", async (original) => ({ ...await original<typeof import("os")>(), homedir: () => fixture.root }));
vi.mock("child_process", () => ({ execFile: fixture.exec }));
vi.mock("./cli-bootstrap", () => ({ managedCliPath: () => join(fixture.root, "fixture-cli"), ensureManagedCli: vi.fn() }));

const profile = "desktop-ipc.test";
const requests: { path: string; authorization?: string; profile?: string | string[] }[] = [];
let server: Server;
let port: number;
let response: unknown;
let status = 200;

async function invoke(name: string, ...args: unknown[]) {
  const handler = fixture.handlers.get(name);
  if (!handler) throw new Error(`IPC handler missing: ${name}`);
  return handler({}, ...args);
}

beforeEach(async () => {
  vi.resetModules();
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  fixture.handlers.clear();
  requests.length = 0;
  response = { worktrees: [] };
  status = 200;
  fixture.root = mkdtempSync(join(tmpdir(), "desktop-capabilities-"));
  writeFileSync(join(fixture.root, "fixture-cli"), "fixture");
  fixture.exec.mockReset().mockImplementation((_bin: string, _args: string[], _options: unknown, done: (error: Error | null, stdout?: string) => void) => done(null, '{"version":"fixture"}'));
  server = createServer((request, result) => {
    const path = request.url ?? "";
    if (path === "/health") {
      result.end(JSON.stringify({ status: "running", profile, pid: process.pid, runtimes: [], process_services: ["ai"] }));
      return;
    }
    requests.push({ path, authorization: request.headers.authorization, profile: request.headers["x-multica-profile"] });
    result.statusCode = status;
    result.end(JSON.stringify(response));
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("Fixture port missing");
  port = address.port;
  const directory = join(fixture.root, ".multica", "profiles", profile);
  mkdirSync(directory, { recursive: true });
  writeFileSync(join(directory, "config.json"), JSON.stringify({ server_url: "https://ipc.test", token: "fixture-token" }));
  writeFileSync(join(directory, "daemon.port"), JSON.stringify({ port, pid: process.pid }));
  const { setupDaemonManager } = await import("./daemon-manager");
  setupDaemonManager(() => null);
  await invoke("daemon:is-cli-installed");
});

afterEach(async () => {
  vi.clearAllTimers();
  vi.useRealTimers();
  await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  rmSync(fixture.root, { recursive: true });
});

it("lists worktrees through the startup registry using the active profile's recorded port and credentials", async () => {
  await invoke("daemon:set-target-api-url", "https://ipc.test");
  expect(await invoke("daemon:list-worktrees")).toEqual([]);
  expect(requests).toEqual([{ path: "/worktrees", authorization: "Bearer fixture-token", profile }]);
});

it("delivers review inventory with verified profile health through startup", async () => {
  await invoke("daemon:set-target-api-url", "https://ipc.test");
  expect(await invoke("daemon:review-inventory")).toMatchObject({ health: { profile }, worktrees: [] });
});

it("returns process rows and the daemon's deduplicated footprint total", async () => {
  await invoke("daemon:set-target-api-url", "https://ipc.test");
  response = { processes: [{ category: "daemon", id: "fixture", name: "Fixture", state: "running", ready: true, memory_bytes: 32 }], total: { available: true, memory_bytes: 64, process_count: 2 } };
  expect(await invoke("daemon:processes-overview")).toEqual(response);
  expect(requests[0]?.path).toBe("/runtimes/processes/overview");
});

it("routes local Daemon capabilities through the authenticated health port", async () => {
  await invoke("daemon:set-target-api-url", "https://ipc.test");
  const capabilities = {
    mcp: [{ name: "identity-actions", state: "ready", ready: true }],
    services: [{ name: "llm2jev" }],
    applications: [{ workspace_id: "ws", runtime_id: "rt", application_id: "app", revision: 1 }],
  };
  response = capabilities;
  expect(await invoke("daemon:mcp-readiness")).toEqual(capabilities.mcp);
  expect(await invoke("daemon:mcp-services")).toEqual({ ...capabilities, instances: [] });
  expect(await invoke("daemon:list-local-applications")).toEqual(capabilities.applications);
  expect(requests.map((request) => request.path)).toEqual([
    "/mcp/readiness",
    "/mcp/services",
    "/applications/local",
  ]);
  expect(requests.every((request) => request.authorization === "Bearer fixture-token" && request.profile === profile)).toBe(true);
});

it("refuses authenticated operations until a desktop profile is selected", async () => {
  await expect(invoke("daemon:list-worktrees")).rejects.toThrow("profile is not ready");
  expect(requests).toEqual([]);
});

it("rejects cleanup input before contacting the daemon", async () => {
  await expect(invoke("daemon:cleanup-worktrees", [], false)).rejects.toThrow("Select between 1 and 1000");
  expect(requests).toEqual([]);
});
