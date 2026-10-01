import { describe, expect, it } from "vitest";
import type { ChatMessage } from "@multica/core/types";
import { findOriginalChatInputMessage } from "./chat-failure-action";

const userMessage = (taskId: string): ChatMessage => ({
  id: "input",
  chat_session_id: "session",
  role: "user",
  content: "retry this request",
  task_id: taskId,
  created_at: new Date(0).toISOString(),
});

describe("findOriginalChatInputMessage", () => {
  it("resolves a retry child failure through its input task id", () => {
    const original = userMessage("root-task");
    const failedChild: ChatMessage = {
      id: "failure",
      chat_session_id: "session",
      role: "assistant",
      content: "provider unavailable",
      task_id: "retry-child-task",
      input_task_id: "root-task",
      created_at: new Date(1).toISOString(),
      failure_reason: "agent_error.provider_network",
    };

    expect(findOriginalChatInputMessage([original, failedChild], failedChild)).toBe(original);
  });

  it("falls back to the message task id for root failures", () => {
    const original = userMessage("root-task");
    const failedRoot: ChatMessage = {
      id: "failure",
      chat_session_id: "session",
      role: "assistant",
      content: "provider unavailable",
      task_id: "root-task",
      created_at: new Date(1).toISOString(),
      failure_reason: "agent_error.provider_network",
    };

    expect(findOriginalChatInputMessage([original, failedRoot], failedRoot)).toBe(original);
  });
});
