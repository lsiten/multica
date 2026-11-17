"use client";

import { useIsMutating, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { chatKeys } from "./queries";

class ChatCancellationReceiptError extends Error {
  constructor(readonly taskId: string, readonly returnedTaskId: string) {
    super("Cancellation did not acknowledge the requested task");
    this.name = "ChatCancellationReceiptError";
  }
}

const cancellationKey = (wsId: string, sessionId: string | null) => ["chat-task-actions", wsId, sessionId, "cancel"] as const;

export function useCancelChatTask(wsId: string, sessionId: string | null) {
  return useMutation({
    mutationKey: cancellationKey(wsId, sessionId),
    retry: false,
    mutationFn: async ({ taskId, options }: {
      readonly taskId: string;
      readonly options?: { readonly queuedAction?: "edit" | "remove"; readonly sessionId?: string };
    }) => {
      const result = await api.cancelTaskById(taskId, options);
      if (result.id !== taskId) throw new ChatCancellationReceiptError(taskId, result.id);
      return result;
    },
  });
}

export function useChatTaskStopping(wsId: string, sessionId: string | null, taskId: string | null) {
  return useIsMutating({
    mutationKey: cancellationKey(wsId, sessionId),
    predicate: (mutation) => {
      const input = mutation.state.variables;
      return !!taskId && typeof input === "object" && input !== null && "taskId" in input && input.taskId === taskId;
    },
  }) > 0;
}

export function useContinueChatTask(wsId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ sessionId, taskId }: {
      readonly sessionId: string;
      readonly taskId: string;
    }) => api.continueChatTask(sessionId, taskId),
    onSuccess: async (_result, { sessionId }) => {
      await Promise.all([
        client.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) }),
        client.invalidateQueries({ queryKey: chatKeys.pendingTasks(wsId) }),
      ]);
    },
  });
}
