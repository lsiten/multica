// @vitest-environment node

import { describe, expect, it } from "vitest";
import { deriveRunReceipt } from "./chat-composer";

describe("deriveRunReceipt", () => {
  it("waits for the persisted assistant message before reporting completion", () => {
    expect(deriveRunReceipt("task-1", [])).toBeNull();
    expect(deriveRunReceipt("task-1", [{ role: "user", task_id: "task-1", failure_reason: null }])).toBeNull();
    expect(deriveRunReceipt("task-1", [{ role: "assistant", task_id: "task-1", failure_reason: null }])).toBe("completed");
  });

  it("reports a failure when the persisted assistant carries failure_reason", () => {
    expect(deriveRunReceipt("task-2", [{ role: "assistant", task_id: "task-2", failure_reason: "runtime_offline" }])).toBe("failed");
  });
});
