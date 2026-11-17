"use client";

import { useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { chatKeys, chatSessionOptions, sortChatSessions } from "@multica/core/chat/queries";
import type { SentMessageReference } from "@multica/core/chat";
import type { ChatSession } from "@multica/core/types";
import { useT } from "../../i18n";

export function useMessageSourceNavigation(workspaceId: string, onOpen: (session: ChatSession, messageId: string) => void) {
  const { t } = useT("chat");
  const client = useQueryClient();
  const currentWorkspace = useRef(workspaceId);
  currentWorkspace.current = workspaceId;
  return (source: SentMessageReference) => {
    if (source.workspaceId !== workspaceId) return;
    void client.fetchQuery(chatSessionOptions(workspaceId, source.sessionId)).then((session) => {
      if (currentWorkspace.current !== workspaceId) return;
      if (session.id !== source.sessionId || session.workspace_id !== workspaceId) {
        toast.error(t(($) => $.message_actions.source_unavailable));
        return;
      }
      client.setQueryData<ChatSession[]>(chatKeys.sessions(workspaceId), (sessions) => sortChatSessions([session, ...(sessions ?? []).filter((entry) => entry.id !== session.id)]));
      onOpen(session, source.messageId);
    }).catch((error: unknown) => {
      if (!(error instanceof Error)) throw error;
      toast.error(t(($) => $.message_actions.source_unavailable));
    });
  };
}
