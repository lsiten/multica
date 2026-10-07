// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { defaultApplicationConfig } from "../applications/schema";
import { setCurrentWorkspace } from "../platform";
import { setSchemaLogger } from "./schema";
import { noopLogger } from "../logger";

const workspaceId = "11111111-1111-4111-8111-111111111111";
const projectId = "22222222-2222-4222-8222-222222222222";
const applicationId = "33333333-3333-4333-8333-333333333333";
const userId = "44444444-4444-4444-8444-444444444444";
const foreignWorkspaceId = "55555555-5555-4555-8555-555555555555";

function application(overrides: Record<string, unknown> = {}) {
  return {
    id: applicationId, workspace_id: workspaceId, project_id: projectId,
    name: "Preview", kind: "service", revision: 1, created_by: userId,
    created_at: "2026-10-06T12:00:00Z", updated_at: "2026-10-06T12:00:00Z",
    config: { ...defaultApplicationConfig(), mode: "external", port: 4100 },
    ...overrides,
  };
}

function respond(body: unknown) {
  const fetch = vi.fn().mockImplementation(async () => new Response(JSON.stringify(body), { status: 200 }));
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

afterEach(() => { vi.unstubAllGlobals(); setCurrentWorkspace(null, null); setSchemaLogger(noopLogger); });

describe("application API boundary", () => {
  it("validates cancellation scope and continues to distinguish cancelling from cancelled", async () => {
    const operationId = foreignWorkspaceId;
    const receipt = { id: operationId, workspace_id: workspaceId, application_id: applicationId, action: "start", actor_type: "member", actor_id: userId,
      state: "cancelling", created_at: "2026-10-07T01:00:00Z", cancel_requested_at: "2026-10-07T01:01:00Z", cancel_actor_type: "member", cancel_actor_id: userId,
      snapshot: { root_runtime_id: projectId, root_revision: 1, placements: {}, plan: { root_id: applicationId, nodes: [], waves: [] } }, steps: [] };
    const fetch = respond(receipt);
    const client = new ApiClient("https://api.test");
    expect((await client.cancelApplicationOperation(workspaceId, applicationId, operationId)).state).toBe("cancelling");
    expect(fetch.mock.calls[0]![1].method).toBe("POST");
    expect(new Headers(fetch.mock.calls[0]![1].headers).get("X-Workspace-ID")).toBe(workspaceId);
    respond({ ...receipt, state: "cancelled" });
    expect((await client.cancelApplicationOperation(workspaceId, applicationId, operationId)).state).toBe("cancelled");
    for (const overrides of [{ id: applicationId }, { workspace_id: foreignWorkspaceId }, { state: "completed" }, { cancel_requested_at: null }]) {
      respond({ ...receipt, ...overrides });
      await expect(client.cancelApplicationOperation(workspaceId, applicationId, operationId)).rejects.toThrow("Invalid application cancellation receipt");
    }
  });

  it("validates isolated launch URLs and never logs malformed ticket payloads", async () => {
    const ticket = "a".repeat(64);
    const url = `https://${applicationId}.apps.test/.__multica/launch?ticket=${ticket}`;
    const client = new ApiClient("https://api.test");
    respond({ url });
    expect(await client.launchApplication(workspaceId, projectId, applicationId)).toBe(url);
    for (const malformed of [url.replace(applicationId, foreignWorkspaceId), url.replace("https:", "javascript:"), url.replace("https://", "https://user:secret@"), url+"#fragment", url+"&token=platform-secret"]) {
      respond({ url: malformed });
      await expect(client.launchApplication(workspaceId, projectId, applicationId)).rejects.toThrow("Invalid application launch");
    }
    const warn = vi.fn();
    setSchemaLogger({ ...noopLogger, warn });
    respond({ url: "bad-url-"+ticket });
    await expect(client.launchApplication(workspaceId, projectId, applicationId)).rejects.toThrow("Invalid application launch receipt");
    expect(warn).toHaveBeenCalled();
    expect(JSON.stringify(warn.mock.calls)).not.toContain(ticket);
  });

  it("rejects malformed log pages instead of displaying invalid output", async () => {
    const client = new ApiClient("https://api.test");
    respond({ text: "output", cursor: "host:0:6", gap: false });
    expect(await client.getApplicationLogs(workspaceId, applicationId, projectId)).toMatchObject({ text: "output", gap: false });
    respond({ text: "output", cursor: null, gap: "yes" });
    await expect(client.getApplicationLogs(workspaceId, applicationId, projectId)).rejects.toThrow("Invalid application log response");
  });

  it("validates board state and rejects data from another workspace", async () => {
    const board = {
      applications: [application()], instances: [], endpoints: [], operations: [],
      summary: { services: 1, compositions: 0, instances: 0, running: 0, unhealthy: 0, offline: 0, stopped: 0, runtimes: 0 },
    };
    respond(board);
    const client = new ApiClient("https://api.test");
    expect((await client.getApplicationBoard(workspaceId)).summary.services).toBe(1);
    respond({ ...board, applications: [application({ workspace_id: foreignWorkspaceId })] });
    await expect(client.getApplicationBoard(workspaceId)).rejects.toThrow("Invalid application board response");
    respond({ ...board, instances: [null] });
    await expect(client.getApplicationBoard(workspaceId)).rejects.toThrow("Invalid application board response");
  });

  it("rejects malformed operation receipts and uses a caller-owned idempotency key", async () => {
    const fetch = respond({ accepted: true });
    await expect(new ApiClient("https://api.test").enqueueApplicationOperation(workspaceId, applicationId, {
      action: "start", revision: 1, runtime_id: userId, idempotency_key: "repeat-this-request",
    })).rejects.toThrow("Invalid application operation receipt");
    expect(JSON.parse(fetch.mock.calls[0]![1].body).idempotency_key).toBe("repeat-this-request");
    expect(new Headers(fetch.mock.calls[0]![1].headers).get("X-Workspace-ID")).toBe(workspaceId);
  });

  it("reads operation deadlines while accepting older servers and malformed optional dates", async () => {
    const operation = {
      id: userId, workspace_id: workspaceId, application_id: applicationId, action: "start", actor_type: "member", actor_id: userId,
      state: "queued", created_at: "2026-10-07T00:00:00Z",
      snapshot: { root_runtime_id: userId, root_revision: 1, placements: {}, plan: { root_id: applicationId, nodes: [], waves: [] } }, steps: [],
    };
    const client = new ApiClient("https://api.test");
    for (const deadline of ["2026-10-07T04:00:00Z", "invalid-date", 42, null, undefined]) {
      respond({ ...operation, ...(deadline === undefined ? {} : { deadline_at: deadline }) });
      const result = await client.getApplicationOperation(workspaceId, applicationId, userId);
      expect(result.deadline_at).toBe(deadline === undefined ? undefined : deadline === "2026-10-07T04:00:00Z" ? deadline : null);
      expect(result.state).toBe("queued");
    }
  });

  it("uses the requested workspace instead of a different active tab", async () => {
    setCurrentWorkspace("foreign-workspace", foreignWorkspaceId);
    const fetch = respond({ applications: [application()], total: 1 });
    const result = await new ApiClient("https://api.test").listApplications(workspaceId, projectId);
    expect(result.applications).toHaveLength(1);
    const [url, init] = fetch.mock.calls[0]!;
    expect(url).toBe(`https://api.test/api/applications/?project_id=${projectId}`);
    expect(new Headers(init.headers).get("X-Workspace-ID")).toBe(workspaceId);
    expect(new Headers(init.headers).get("X-Workspace-Slug")).toBe("");
  });

  it("defaults optional fields and preserves unknown enum values as unknown", async () => {
    respond(application({ kind: "future-kind", config: { ...defaultApplicationConfig(), mode: "future-mode" } }));
    const result = await new ApiClient("https://api.test").getApplication(workspaceId, applicationId);
    expect(result).toMatchObject({ kind: "unknown", description: "", relations: [], config: { mode: "unknown" } });
  });

  it.each([
    { applications: [null], total: 1 },
    { applications: [application({ workspace_id: foreignWorkspaceId })], total: 1 },
    { applications: [application({ project_id: foreignWorkspaceId })], total: 1 },
    { applications: [], total: -1 },
  ])("rejects malformed or incorrectly scoped lists", async (body) => {
    respond(body);
    await expect(new ApiClient("https://api.test").listApplications(workspaceId, projectId)).rejects.toThrow("Invalid application list response");
  });

  it("rejects a detail response for another application", async () => {
    respond(application({ id: foreignWorkspaceId }));
    await expect(new ApiClient("https://api.test").getApplication(workspaceId, applicationId)).rejects.toThrow("Invalid application response");
  });

  it("does not treat an invalid mutation receipt as success", async () => {
    const client = new ApiClient("https://api.test");
    respond({ accepted: true });
    await expect(client.createApplication(workspaceId, { project_id: projectId, name: "Preview", kind: "service", config: defaultApplicationConfig() })).rejects.toThrow("Invalid application create receipt");
    respond(application());
    await expect(client.updateApplication(workspaceId, applicationId, { revision: 1, name: "Changed" })).rejects.toThrow("Invalid application update receipt");
    respond(application({ revision: 2 }));
    expect(await client.updateApplication(workspaceId, applicationId, { revision: 1, name: "Changed" })).toMatchObject({ revision: 2 });
  });

  it("sends deletion revision and validates plan identity", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetch);
    const client = new ApiClient("https://api.test");
    await client.deleteApplication(workspaceId, applicationId, 3);
    expect(fetch.mock.calls[0]![0]).toBe(`https://api.test/api/applications/${applicationId}?revision=3`);
    expect(fetch.mock.calls[0]![1].method).toBe("DELETE");
    respond({ root_id: foreignWorkspaceId, nodes: [], waves: [] });
    await expect(client.previewApplicationPlan(workspaceId, applicationId)).rejects.toThrow("Invalid application plan response");
  });
});
