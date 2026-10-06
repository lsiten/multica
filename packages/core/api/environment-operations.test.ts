// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { setCurrentWorkspace } from "../platform";

afterEach(() => { vi.unstubAllGlobals(); setCurrentWorkspace(null, null); });

it("routes inventory to the selected runtime and overrides the current workspace headers", async () => {
  setCurrentWorkspace("current-slug", "current-id");
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify([]), { status: 200 }));
  vi.stubGlobal("fetch", fetch);
  const result = await new ApiClient("https://example.test").executeRuntimeEnvironment("target-id", "runtime-id", { action: "inventory" });
  expect(result).toEqual([]);
  const [url, init] = fetch.mock.calls[0]!;
  expect(url).toBe("https://example.test/api/runtimes/runtime-id/environments/execute");
  expect(new Headers(init.headers).get("X-Workspace-ID")).toBe("target-id");
  expect(new Headers(init.headers).get("X-Workspace-Slug")).toBe("");
  expect(JSON.parse(init.body)).toEqual({ action: "inventory" });
});

it("rejects malformed operation receipts and caller-supplied filesystem paths", async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: "completed" }), { status: 200 }));
  vi.stubGlobal("fetch", fetch);
  const client = new ApiClient("https://example.test");
  await expect(client.executeRuntimeEnvironment("ws", "runtime", { action: "operation_status", operation_id: "a".repeat(64) })).rejects.toThrow("Invalid environment operation receipt");
  fetch.mockClear();
  await expect(client.executeRuntimeEnvironment("ws", "runtime", { action: "operation_status", operation_id: "/tmp/root" })).rejects.toThrow();
  expect(fetch).not.toHaveBeenCalled();
});

it("parses runtime choices at the API boundary and treats unknown availability as offline", async () => {
  setCurrentWorkspace("current", "current-id");
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify([{ id: "runtime", workspace_id: "target", name: "Mac", owner_id: "user", runtime_mode: "local", status: "future" }]), { status: 200 }));
  vi.stubGlobal("fetch", fetch);
  const client = new ApiClient("https://example.test");
  expect(await client.listEnvironmentRuntimes("target", "target-slug")).toEqual([expect.objectContaining({ status: "offline" })]);
  const [, init] = fetch.mock.calls[0]!;
  expect(new Headers(init.headers).get("X-Workspace-ID")).toBe("target");
  expect(new Headers(init.headers).get("X-Workspace-Slug")).toBe("target-slug");
  fetch.mockResolvedValue(new Response(JSON.stringify([null]), { status: 200 }));
  await expect(client.listEnvironmentRuntimes("target", "target-slug")).rejects.toThrow("Invalid environment runtime list");
});

it("validates scoped policy read and update responses", async () => {
  const policy = { enabled: true, archive_after_hours: 24, cache_after_hours: 12, pressure_cache_after_hours: 1, max_idle_environments: 100, max_directory_bytes: 20 * 1024 ** 3, minimum_free_bytes: 5 * 1024 ** 3 };
  const response = { workspace_id: "ws", runtime_id: "runtime", policy, effective_enabled: true, scan_interval_seconds: 300, free_bytes: null, last_scan_at: null, idle_environments: 0, directory_bytes: 0, under_pressure: false };
  const fetch = vi.fn().mockImplementation(async () => new Response(JSON.stringify(response), { status: 200 }));
  vi.stubGlobal("fetch", fetch);
  const client = new ApiClient("https://example.test");
  expect(await client.executeRuntimeEnvironment("ws", "runtime", { action: "policy" })).toEqual({ ...response, task_retention_supported: false });
  expect(await client.executeRuntimeEnvironment("ws", "runtime", { action: "policy_update", policy })).toEqual({ ...response, task_retention_supported: false });
  expect(JSON.parse(fetch.mock.calls[1]![1].body)).toEqual({ action: "policy_update", policy });
  fetch.mockResolvedValue(new Response("{}", { status: 200 }));
  await expect(client.executeRuntimeEnvironment("ws", "runtime", { action: "policy" })).rejects.toThrow("Invalid environment policy response");
});
