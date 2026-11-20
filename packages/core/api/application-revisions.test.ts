// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { defaultApplicationConfig } from "../applications/schema";
import { setSchemaLogger } from "./schema";
import { noopLogger } from "../logger";

const workspaceId = "11111111-1111-4111-8111-111111111111";
const applicationId = "22222222-2222-4222-8222-222222222222";
const otherId = "33333333-3333-4333-8333-333333333333";
const scope = { workspaceId, applicationId };
const revision = { workspace_id: workspaceId, application_id: applicationId, revision: 3, config: defaultApplicationConfig(), relations: [], actor_type: "member", actor_id: workspaceId, created_at: "2026-11-20T00:00:00Z" };
const client = new ApiClient("https://api.test");

function respond(body: unknown) {
  const transport = vi.fn<typeof fetch>(async () => new Response(JSON.stringify(body), { status: 200 }));
  vi.stubGlobal("fetch", transport);
  return transport;
}
afterEach(() => { vi.unstubAllGlobals(); setSchemaLogger(noopLogger); });

it("reads descending revision pages under the explicit workspace and an exclusive cursor", async () => {
  const transport = respond({ revisions: [revision, { ...revision, revision: 2 }], next_before_revision: 2 });
  const page = await client.applicationRevisions.list(scope, { beforeRevision: 4 });
  expect(page.revisions.map((item) => item.revision)).toEqual([3, 2]);
  expect(page.next_before_revision).toBe(2);
  const call = transport.mock.calls[0];
  expect(call?.[0]).toBe(`https://api.test/api/applications/${applicationId}/revisions?before_revision=4`);
  expect(new Headers(call?.[1]?.headers).get("X-Workspace-ID")).toBe(workspaceId);
});

it.each([
  { revisions: null, next_before_revision: null },
  { revisions: [{ ...revision, workspace_id: otherId }], next_before_revision: null },
  { revisions: [{ ...revision, application_id: otherId }], next_before_revision: null },
  { revisions: [revision, revision], next_before_revision: null },
  { revisions: [{ ...revision, revision: 2 }, revision], next_before_revision: null },
  { revisions: [revision], next_before_revision: 4 },
  { revisions: [], next_before_revision: 1 },
  { revisions: [{ ...revision, config: null }], next_before_revision: null },
  { revisions: [revision] },
])("rejects malformed or mis-scoped revision history %#", async (body) => {
  respond(body);
  await expect(client.applicationRevisions.list(scope)).rejects.toThrow("Invalid application history response");
});

it("rejects a history cursor that repeats the previous page", async () => {
  respond({ revisions: [revision], next_before_revision: null });
  await expect(client.applicationRevisions.list(scope, { beforeRevision: 3 })).rejects.toThrow("Invalid application history response");
});

it.each([
  { ...revision, workspace_id: otherId },
  { ...revision, application_id: otherId },
  { ...revision, revision: 2 },
  { ...revision, created_at: null },
])("rejects a revision that does not match the requested identity %#", async (body) => {
  respond(body);
  await expect(client.applicationRevisions.read(scope, 3)).rejects.toThrow("Invalid application revision response");
});

it("keeps a readable revision when a newer server introduces an actor type", async () => {
  respond({ ...revision, actor_type: "scheduled_automation" });
  expect((await client.applicationRevisions.read(scope, 3)).actor_type).toBe("unknown");
});

it("restores config only after an incremented, scoped revision receipt", async () => {
  const application = { id: applicationId, workspace_id: workspaceId, project_id: otherId, name: "Preview", kind: "service", revision: 4, created_by: workspaceId, created_at: revision.created_at, updated_at: revision.created_at, config: revision.config, relations: [] };
  respond(application);
  expect((await client.applicationRevisions.restore(scope, { revision: 3, target_revision: 1 })).revision).toBe(4);
  for (const overrides of [{ revision: 3 }, { id: otherId }, { workspace_id: otherId }, { config: null }]) {
    respond({ ...application, ...overrides });
    await expect(client.applicationRevisions.restore(scope, { revision: 3, target_revision: 1 })).rejects.toThrow("Invalid application restore response");
  }
});
