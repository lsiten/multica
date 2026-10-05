// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { IssueProgressViewSchema, ProgressActionResultSchema } from "./issue-progress";

function response() {
  return {
    workspace_id: "ws", scope: { type: "issue", id: "root" }, as_of: "2026-10-05T00:00:00Z", version: "v1",
    summary: { total: 2, done: 1, closed: 0, open: 1, open_leaf: 1, open_parent: 0, attention: 1, pending_decisions: 0 },
    items: [{ issue: { id: "child", identifier: "MUL-2", title: "Child", status: "future" }, reasons: ["new_server_reason"] }],
    filtered_total: 1,
  };
}

afterEach(() => vi.unstubAllGlobals());

describe("work progress response boundary", () => {
  it("defaults optional additions and preserves unknown status and reason codes", () => {
    const parsed = IssueProgressViewSchema.parse(response());
    expect(parsed.complete).toBe(false);
    expect(parsed.root).toBeNull();
    expect(parsed.items[0]?.issue.status_category).toBe("unknown");
    expect(parsed.items[0]?.reasons).toEqual(["new_server_reason"]);
    expect(parsed.items[0]?.direct_blockers).toEqual([]);
  });

  it.each([
    { ...response(), summary: undefined },
    { ...response(), summary: { ...response().summary, open: 0 } },
    { ...response(), items: "invalid" },
    { ...response(), has_more: true },
    { ...response(), workspace_id: "" },
  ])("rejects corrupted summaries instead of inventing completion", invalid => {
    expect(IssueProgressViewSchema.safeParse(invalid).success).toBe(false);
  });

  it("throws when a successful HTTP response contains invalid work data", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [] }), { status: 200, headers: { "Content-Type": "application/json" } })));
    await expect(new ApiClient("https://api.test").getWorkProgress({ type: "issue", id: "root" })).rejects.toThrow("malformed");
  });

  it("encodes identifiers, filters, cursor and the viewer's calendar date", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(response()), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetch);
    await new ApiClient("https://api.test").getWorkProgress({ type: "project", id: "project/a" }, { filter: "blocked", assignee_type: "agent", assignee_id: "agent", mine: true, limit: 50 }, "cursor/one");
    const url = new URL(fetch.mock.calls[0]?.[0]);
    expect(url.pathname).toBe("/api/projects/project%2Fa/attention");
    expect(url.searchParams.get("cursor")).toBe("cursor/one");
    expect(url.searchParams.get("mine")).toBe("true");
    expect(url.searchParams.get("today")).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });
});


describe("action projection compatibility", () => {
 it("keeps counts and existing task links when optional action metadata is malformed", () => {
  const raw=response();const parsed=IssueProgressViewSchema.parse({...raw,items:[{...raw.items[0],next_step:{kind:42},actions:"invalid"}]});
  expect(parsed.items[0]!.next_step).toBeNull();expect(parsed.items[0]!.actions).toEqual([]);expect(parsed.summary.open).toBe(1);
 });
});

it("rejects malformed action receipts",()=>{
 expect(ProgressActionResultSchema.safeParse({task_id:"unbound",issue_id:"invalid",status:"queued"}).success).toBe(false);
});

it("never enables a future action whose meaning this client cannot show",()=>{
 const raw=response();const parsed=IssueProgressViewSchema.parse({...raw,items:[{...raw.items[0],actions:[{kind:"future",actor_type:"unknown",actor_id:null,enabled:true}]}]});expect(parsed.items[0]!.actions![0]).toMatchObject({kind:"unknown",enabled:false,disabled_reason:"unsupported_action"});
});
