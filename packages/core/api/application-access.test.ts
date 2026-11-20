// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

const workspaceId = "11111111-1111-4111-8111-111111111111";
const applicationId = "22222222-2222-4222-8222-222222222222";
const endpointId = "33333333-3333-4333-8333-333333333333";

afterEach(() => vi.unstubAllGlobals());

it("creates a scoped service access credential and preserves its expiry", async () => {
  const fetchMock = vi.fn<typeof globalThis.fetch>(async () => new Response(JSON.stringify({ url: "https://app.example/service", token: "mas_secret", expires_in_seconds: 28800 }), { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  const access = await new ApiClient("https://api.test").createApplicationServiceAccess(workspaceId, applicationId, endpointId);
  expect(access).toEqual({ url: "https://app.example/service", token: "mas_secret", expires_in_seconds: 28800 });
  expect(fetchMock.mock.calls[0]?.[0]).toBe(`https://api.test/api/applications/${applicationId}/endpoints/${endpointId}/service-access`);
  expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get("X-Workspace-ID")).toBe(workspaceId);
});

it("rejects a malformed access receipt", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ url: "not-a-url", token: "" }), { status: 200 })));
  await expect(new ApiClient("https://api.test").createApplicationServiceAccess(workspaceId, applicationId, endpointId)).rejects.toThrow("Invalid application service access response");
});
