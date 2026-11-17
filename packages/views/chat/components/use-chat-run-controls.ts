"use client";

import { useCallback } from "react";
import { toast } from "sonner";
import { useChatRunPolicyStore, useContinueChatTask } from "@multica/core/chat";
import type { ChatAutonomyPolicyOverride, ChatMessage } from "@multica/core/types";
import { useT } from "../../i18n";
import { findOriginalChatInputMessage } from "./chat-failure-action";
import type { useChatController } from "./use-chat-controller";

type RunController = Pick<ReturnType<typeof useChatController>,
  "wsId" | "activeSessionId" | "messages" | "handleSend"
> & { readonly activeAgent: { readonly id: string } | null | undefined };

export function useChatRunControls(controller: RunController) {
  const { t } = useT("chat");
  const scope = JSON.stringify([
    controller.wsId, controller.activeSessionId, controller.activeAgent?.id,
  ]);
  const autonomyPolicy = useChatRunPolicyStore((state) => state.policies[scope] ?? null);
  const setPolicy = useChatRunPolicyStore((state) => state.setPolicy);
  const consumePolicy = useChatRunPolicyStore((state) => state.consumePolicy);
  const continuation = useContinueChatTask(controller.wsId);
  const { handleSend: send, messages } = controller;

  const setAutonomyPolicy = useCallback((policy: ChatAutonomyPolicyOverride | null) => {
    setPolicy(scope, policy);
  }, [scope, setPolicy]);

  const handleSend = useCallback(async (...args: Parameters<typeof send>) => {
    const accepted = await send(...args);
    if (accepted) consumePolicy(scope, autonomyPolicy);
    return accepted;
  }, [send, scope, autonomyPolicy, consumePolicy]);

  const handleFailureAction = useCallback(async (
    message: ChatMessage,
    action: "retry" | "continue",
  ) => {
    switch (action) {
      case "retry": {
        const original = findOriginalChatInputMessage(messages, message);
        if (!original) {
          toast.error(t(($) => $.input.send_failed_toast));
          return;
        }
        await send(original.content, original.attachments?.map((attachment) => attachment.id));
        return;
      }
      case "continue": {
        if (!message.task_id) {
          toast.error(t(($) => $.input.send_failed_toast));
          return;
        }
        try {
          await continuation.mutateAsync({ sessionId: message.chat_session_id, taskId: message.task_id });
        } catch (error) {
          if (!(error instanceof Error)) throw error;
          toast.error(t(($) => $.input.send_failed_toast));
        }
        return;
      }
      default: {
        const unhandled: never = action;
        return unhandled;
      }
    }
  }, [messages, send, continuation, t]);

  return { autonomyPolicy, setAutonomyPolicy, handleSend, handleFailureAction };
}
