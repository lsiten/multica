// @vitest-environment node
import { expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { ApiClient } from "../api";
import { applicationKeys, applicationLogsOptions, applicationPlanOptions, applicationBoardOptions, applicationOperationOptions } from "./queries";
import { applicationOperationSchema } from "./schema";

vi.mock("../api", () => ({ api: { getApplicationLogs: readApplicationLogs } }));
const readApplicationLogs = vi.hoisted(() => vi.fn<ApiClient["getApplicationLogs"]>());

it("uses slower safety polls for idle boards and stops terminal operation polling", () => {
  const client = new QueryClient();
  const boardOptions = applicationBoardOptions("workspace");
  const operationOptions = applicationOperationOptions("workspace", "app", "operation");
  const board = client.getQueryCache().build(client, { queryKey: applicationKeys.board("workspace"), queryFn: boardOptions.queryFn });
  const operation = client.getQueryCache().build(client, { queryKey: applicationKeys.operation("workspace", "app", "operation"), queryFn: operationOptions.queryFn });
  const boardInterval = boardOptions.refetchInterval;
  const operationInterval = operationOptions.refetchInterval;
  if (typeof boardInterval !== "function" || typeof operationInterval !== "function") throw new Error("Missing state-aware polling");
  try {
    const id = "11111111-1111-4111-8111-111111111111";
    const completed = applicationOperationSchema.parse({ id, workspace_id: id, application_id: id, action: "start", actor_type: "member", actor_id: id, state: "completed", created_at: "2026-10-12T00:00:00Z", snapshot: { root_runtime_id: id, root_revision: 1, placements: {}, plan: { root_id: id, nodes: [], waves: [] } } });
    const data = { applications: [], instances: [], endpoints: [], operations: [completed], summary: { services: 0, compositions: 0, instances: 0, running: 0, unhealthy: 0, offline: 0, stopped: 0, runtimes: 0 } };
    board.setData(data);
    operation.setData(completed);
    expect(boardInterval(board)).toBe(30_000);
    expect(operationInterval(operation)).toBe(false);
    const running = { ...completed, state: "running" as const };
    board.setData({ ...data, operations: [running] });
    operation.setData(running);
    expect(boardInterval(board)).toBe(5000);
    expect(operationInterval(operation)).toBe(5000);
    expect(boardOptions.refetchIntervalInBackground).toBe(false);
    expect(operationOptions.refetchIntervalInBackground).toBe(false);
  } finally { client.clear(); }
});

it("separates workspace, project and graph revision caches", () => {
  expect(applicationKeys.list("first", "project")).not.toEqual(applicationKeys.list("second", "project"));
  expect(applicationKeys.list("first", "project")).not.toEqual(applicationKeys.list("first"));
  expect(applicationPlanOptions("first", "application", 1).queryKey).not.toEqual(applicationPlanOptions("first", "application", 2).queryKey);
});

it("continues local log cursors without duplicating output and marks rotation gaps", async () => {
  const read = readApplicationLogs.mockReset()
    .mockResolvedValueOnce({ text: "first\n", cursor: "host:0:6", gap: false })
    .mockResolvedValueOnce({ text: "second\n", cursor: "host:0:13", gap: false })
    .mockResolvedValueOnce({ text: "", cursor: "host:0:13", gap: false })
    .mockResolvedValueOnce({ text: "new rotation\n", cursor: "host:1:13", gap: true });
  const client = new QueryClient();
  try {
    const options = applicationLogsOptions("workspace", "app", "instance", client);
    expect((await client.fetchQuery(options)).text).toBe("first\n");
    expect((await client.fetchQuery(options)).text).toBe("first\nsecond\n");
    expect(await client.fetchQuery(options)).toMatchObject({ text: "first\nsecond\n", lastPageHasText: false });
    expect(await client.fetchQuery(options)).toMatchObject({ text: "new rotation\n", gap: true });
    expect(read.mock.calls.map((call) => call[3])).toEqual(["", "host:0:6", "host:0:13", "host:0:13"]);
  } finally { client.clear(); read.mockReset(); }
});

it("bounds displayed logs independently for every instance and workspace", async () => {
  const read = readApplicationLogs.mockReset().mockResolvedValue({ text: "x".repeat(256 * 1024 + 1), cursor: "host:0:262145", gap: false });
  const client = new QueryClient();
  try {
    const first = applicationLogsOptions("workspace", "app", "first", client);
    const result = await client.fetchQuery(first);
    expect(result.text).toHaveLength(256 * 1024);
    expect(result.truncated).toBe(true);
    const other = applicationLogsOptions("workspace", "app", "other", client);
    await client.fetchQuery(other);
    expect(read.mock.calls[1]?.[3]).toBe("");
    expect(other.queryKey).not.toEqual(first.queryKey);
    expect(applicationLogsOptions("other-workspace", "app", "first", client).queryKey).not.toEqual(first.queryKey);
  } finally { client.clear(); read.mockReset(); }
});
