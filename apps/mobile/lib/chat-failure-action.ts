import type { ChatMessage } from "@multica/core/types";

/** Find the user input that produced a failed assistant message. */
export function findOriginalChatInputMessage(
  messages: readonly ChatMessage[],
  failedMessage: Pick<ChatMessage, "input_task_id" | "task_id">,
): ChatMessage | undefined {
  const inputTaskId = failedMessage.input_task_id ?? failedMessage.task_id;
  if (!inputTaskId) return undefined;

  return messages.find(
    (candidate) => candidate.role === "user" && candidate.task_id === inputTaskId,
  );
}
