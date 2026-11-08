// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ApplicationOperationRequest } from "@multica/core/applications";
import { applicationRequestKey } from "./application-request-key";

const request = { action: "start", revision: 1, runtime_id: "runtime-a", placements: { first: "runtime-a", second: "runtime-b" }, force: false } satisfies Omit<ApplicationOperationRequest, "idempotency_key">;
const intent = { workspaceId: "workspace-a", applicationId: "app-a", request };

describe("application operation identity", () => {
  it("retains a retry key when placement entries have the same values in a different order", () => {
    const first = applicationRequestKey(null, intent);
    const reordered = { ...intent, request: { ...request, placements: { second: "runtime-b", first: "runtime-a" } } };
    expect(applicationRequestKey(first, reordered).key).toBe(first.key);
  });

  it.each([
    { ...intent, workspaceId: "workspace-b" },
    { ...intent, applicationId: "app-b" },
    { ...intent, request: { ...request, action: "restart" as const } },
    { ...intent, request: { ...request, revision: 2 } },
    { ...intent, request: { ...request, runtime_id: "runtime-b" } },
    { ...intent, request: { ...request, force: true } },
    { ...intent, request: { ...request, placements: { ...request.placements, first: "runtime-c" } } },
  ])("assigns a new key for changed operation intent %j", (changed) => {
    const first = applicationRequestKey(null, intent);
    expect(applicationRequestKey(first, changed).key).not.toBe(first.key);
  });
});
