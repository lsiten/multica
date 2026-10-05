// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { isProgressInvalidationEvent, issueProgressKeys, issueProgressOptions, workProgressInfiniteOptions } from "./index";
import { api } from "../api";

vi.mock("../api", () => ({ api: { getWorkProgress: vi.fn() } }));

describe("work progress queries", () => {
  it("isolates workspace, member, target and filter caches", () => {
    const scope = { type: "project" as const, id: "project" };
    expect(issueProgressKeys.view("ws", "me", scope, { mine: true })).not.toEqual(issueProgressKeys.view("ws", "other", scope, { mine: true }));
    expect(issueProgressKeys.all("ws")).not.toEqual(issueProgressKeys.all("other"));
  });

  it.each(["issue:created", "issue:updated", "issue:deleted", "issue_status:changed", "agent:status", "agent:archived", "agent:restored", "task:queued", "task:running", "task:waiting_local_directory", "task:failed", "task:cancelled", "human-request:changed", "project:updated", "project_supervision:updated"])("refreshes authoritative state after %s", event => {
    expect(isProgressInvalidationEvent(event)).toBe(true);
  });

  it.each(["task:message", "task:progress", "reaction:added", "comment:created"])("does not refetch for streamed or unrelated %s", event => {
    expect(isProgressInvalidationEvent(event)).toBe(false);
  });

  it("rejects a response that belongs to another workspace", async () => {
    vi.mocked(api.getWorkProgress).mockResolvedValue({ workspace_id: "other", scope: { type: "issue", id: "root" } } as never);
    const options = issueProgressOptions("ws", "me", "root");
    const query = options.queryFn;
    if (typeof query !== "function") throw new Error("Missing query function");
    await expect(query({ signal: new AbortController().signal } as never)).rejects.toThrow("another scope");
  });

  it("does not retry an unsupported view or a changed pagination snapshot", () => {
    const options = workProgressInfiniteOptions("ws", "me", { type: "project", id: "project" }, {});
    if (typeof options.retry !== "function") throw new Error("Missing retry policy");
    expect(options.retry(0, Object.assign(new Error("unsupported"), { status: 404 }))).toBe(false);
    expect(options.retry(0, Object.assign(new Error("changed"), { status: 409 }))).toBe(false);
    expect(options.retry(0, new Error("temporary"))).toBe(true);
  });
});
