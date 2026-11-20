// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { applicationOperationSchema } from "./schema";
import { ApplicationBulkOperationError, useApplicationBulkOperation } from "./mutations";

const fixture = vi.hoisted(() => ({ enqueue: vi.fn() }));
vi.mock("../api", () => ({ api: { enqueueApplicationOperation: fixture.enqueue } }));
const id = "11111111-1111-4111-8111-111111111111";
const item = { applicationId: id, revision: 1, runtimeId: id };
const receipt = applicationOperationSchema.parse({ id, workspace_id: id, application_id: id, action: "restart", actor_type: "member", actor_id: id, state: "queued", created_at: "2026-11-20T00:00:00Z", snapshot: { root_runtime_id: id, root_revision: 1, placements: {}, plan: { root_id: id, nodes: [], waves: [] } }, steps: [] });
const clients: QueryClient[] = [];

afterEach(() => { clients.forEach((client) => client.clear()); clients.length = 0; vi.resetAllMocks(); });

function wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  clients.push(client);
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

it("returns a partial receipt when some selected instances are accepted", async () => {
  fixture.enqueue.mockResolvedValueOnce(receipt).mockRejectedValueOnce(new Error("offline"));
  const { result } = renderHook(() => useApplicationBulkOperation("workspace"), { wrapper });
  let summary: Awaited<ReturnType<typeof result.current.mutateAsync>> | undefined;
  await act(async () => { summary = await result.current.mutateAsync({ action: "restart", items: [item, { ...item, runtimeId: "22222222-2222-4222-8222-222222222222" }] }); });
  expect(summary?.succeeded).toEqual([id]);
  expect(summary?.failed).toHaveLength(1);
  expect(fixture.enqueue).toHaveBeenCalledTimes(2);
});

it("throws a typed error when every selected instance fails", async () => {
  fixture.enqueue.mockRejectedValue(new Error("offline"));
  const { result } = renderHook(() => useApplicationBulkOperation("workspace"), { wrapper });
  await act(async () => { await expect(result.current.mutateAsync({ action: "stop", items: [item] })).rejects.toBeInstanceOf(ApplicationBulkOperationError); });
  await waitFor(() => expect(result.current.isError).toBe(true));
});
