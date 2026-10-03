// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

const summary = { id: "decision-1", workspace_id: "workspace-1", task_id: "task-1", agent_id: "agent-1", agent_name: "Reviewer", tool: "multica_jev_systemone", source: "local", model: "model", result_class: "success", started_at: "2026-10-03T12:00:00Z" };
const page = { items: [summary], total: 21, limit: 20, offset: 0, as_of: "2026-10-03T12:01:00Z" };
function response(body: unknown) {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetch);
  return fetch;
}
afterEach(() => vi.unstubAllGlobals());

describe("Jev decision log API", () => {
  it("encodes pagination, search and filters and supplies optional defaults", async () => {
    const fetch = response(page);
    const result = await new ApiClient("https://example.test").listJevDecisionLogs("workspace-1", { q: "report & review", agent: "agent-1", source: "local", result_class: "success", limit: 20, offset: 20, as_of: page.as_of });
    const url = new URL(String(fetch.mock.calls[0]![0]));
    expect(url.pathname).toBe("/api/workspaces/workspace-1/jev-decision-logs");
    expect(url.searchParams.get("q")).toBe("report & review");
    expect(url.searchParams.get("offset")).toBe("20");
    expect(url.searchParams.get("as_of")).toBe(page.as_of);
    expect(result.items[0]).toMatchObject({ error_code: "", duration_ms: 0 });
  });
  it("accepts future result and source values with explicit unknown fallbacks", async () => {
    response({ ...page, items: [{ ...summary, result_class: "future-result", source: "future-source" }] });
    const result = await new ApiClient("https://example.test").listJevDecisionLogs("workspace-1");
    expect(result.items[0]).toMatchObject({ result_class: "unknown", source: "unknown" });
  });
  it.each([{ ...page, total: -1 }, { ...page, limit: 0 }, { ...page, items: [{}] }, { ...page, items: [{ ...summary, workspace_id: "another-workspace" }] }])("rejects malformed or cross-workspace pages", async (body) => {
    response(body);
    await expect(new ApiClient("https://example.test").listJevDecisionLogs("workspace-1")).rejects.toThrow("Invalid Jev decision log response");
  });
  it("reads details and rejects mismatched decision identity", async () => {
    response({ ...summary, input: "{}", output: "{}" });
    const client = new ApiClient("https://example.test");
    expect(await client.getJevDecisionLog("workspace-1", "decision-1")).toMatchObject({ requests: [], completed_at: null });
    response({ ...summary, id: "another-decision" });
    await expect(client.getJevDecisionLog("workspace-1", "decision-1")).rejects.toThrow("Invalid Jev decision log detail response");
  });
});
