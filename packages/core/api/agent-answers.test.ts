// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { createAgentCollaborationApi, parseAgentAnswerPage } from "./agent-answers";

const page = {
  task_id: "run-b", run_status: "running", next_cursor: "8:4096", has_more: true,
  messages: [{ id: "message", seq: 8, type: "text", content: "新增回复", offset: 0, has_more_content: true }],
};

describe("agent answer page boundary", () => {
  it("preserves partial-answer continuation and converts wire names", () => {
    expect(parseAgentAnswerPage(page, "run-b")).toEqual({
      taskId: "run-b", runStatus: "running", nextCursor: "8:4096", hasMore: true,
      messages: [{ id: "message", seq: 8, type: "text", content: "新增回复", offset: 0, hasMoreContent: true, createdAt: "" }],
    });
  });

  it.each([
    null,
    { ...page, next_cursor: 5 },
    { ...page, next_cursor: "-1:0" },
    { ...page, has_more: "yes" },
    { ...page, messages: [{ ...page.messages[0], content: null }] },
    { ...page, task_id: "another-run" },
  ])("rejects malformed or misrouted pages without advancing the cursor: %j", (raw) => {
    expect(parseAgentAnswerPage(raw, "run-b")).toBeNull();
  });

  it("keeps an unknown server run state readable", () => {
    expect(parseAgentAnswerPage({ ...page, run_status: "future_state" }, "run-b")?.runStatus).toBe("future_state");
  });

  it("uses the same stable request body for command retries", async () => {
    const fetch = vi.fn(async () => ({
      operation_id: "op-1", operation: "delegate", status: "accepted",
      result: { task_id: "run-2" },
    }));
    const api = createAgentCollaborationApi(fetch);
    await api.delegate("issue/1", { targetAgentId: "agent-2", content: "审查", clientRequestId: "req-1" });
    expect(fetch).toHaveBeenCalledWith("/api/issues/issue%2F1/collaboration/delegate", expect.objectContaining({
      method: "POST", body: JSON.stringify({ target_agent_id: "agent-2", content: "审查", client_request_id: "req-1" }),
    }));
  });

  it("reconciles a receipt after an accepted response is lost", async () => {
    const fetch = vi.fn(async () => ({
      operation_id: "op-1", operation: "delegate", status: "accepted",
      target_task_id: "run-2", result: { task_id: "run-2" },
    }));
    const operation = await createAgentCollaborationApi(fetch).operation("op-1");
    expect(fetch).toHaveBeenCalledWith("/api/agent-collaboration/operations/op-1", { signal: undefined });
    expect(operation.targetTaskId).toBe("run-2");
  });
});
