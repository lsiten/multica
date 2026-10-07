// @vitest-environment node
import { expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { ApiClient } from "../api";
import { applicationKeys, applicationLogsOptions, applicationPlanOptions } from "./queries";

vi.mock("../api", () => ({ api: { getApplicationLogs: readApplicationLogs } }));
const readApplicationLogs = vi.hoisted(() => vi.fn<ApiClient["getApplicationLogs"]>());

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
