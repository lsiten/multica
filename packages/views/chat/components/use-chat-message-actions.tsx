"use client";

import { useCallback, useRef, useState } from "react";
import { useChatReferenceDraftStore, type ChatReference } from "@multica/core/chat";
import { createSafeId } from "@multica/core/utils";
import type { ChatMessage } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useLocale, useT } from "../../i18n";
import { canonicalAnswerText } from "../lib/copy-text";
import type { ChatController } from "./use-chat-controller";

type Controller = Pick<ChatController, "wsId" | "activeSessionId" | "activeAgent" | "isAgentArchived" | "isAgentAccessRevoked" | "isSessionArchived" | "handleStartNewChat" | "requestInputFocus">;

export function useChatMessageActions(c: Controller, startNewChat = c.handleStartNewChat) {
  const { t } = useT("chat");
  const locale = useLocale();
  const [selected, setSelected] = useState<ChatMessage | null>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const { activeSessionId, requestInputFocus } = c;
  const discussion = selected?.chat_session_id === c.activeSessionId ? selected : null;
  const canDiscuss = !!c.activeAgent && !c.isAgentArchived && !c.isAgentAccessRevoked;
  const makeReference = useCallback((message: ChatMessage): ChatReference => {
    const created = new Date(message.created_at);
    const label = t(($) => message.role === "user" ? $.message_actions.user_reference : $.message_actions.assistant_reference);
    return {
      id: createSafeId(),
      label: Number.isNaN(created.getTime()) ? label : `${label} · ${created.toLocaleString(locale, { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}`,
      content: [canonicalAnswerText(message), ...(message.attachments ?? []).map((attachment) => `${attachment.filename} (attachment: ${attachment.id})`)].filter(Boolean).join("\n\n"),
      messageSource: { workspaceId: c.wsId, sessionId: message.chat_session_id, messageId: message.id, role: message.role, createdAt: message.created_at, authorId: message.author_id ?? undefined, attachmentIds: message.attachments?.map((attachment) => attachment.id) },
    };
  }, [c.wsId, locale, t]);
  const addReference = useCallback((sessionId: string | null, message: ChatMessage) => {
    const scope = JSON.stringify([c.wsId, sessionId]);
    const reference = makeReference(message);
    const store = useChatReferenceDraftStore.getState();
    const quoted = store.byScope[scope]?.some((existing) => existing.messageSource?.sessionId === message.chat_session_id && existing.messageSource.messageId === message.id && existing.content === reference.content);
    if (!quoted) store.add(scope, reference);
  }, [c.wsId, makeReference]);
  const quoteMessage = useCallback((message: ChatMessage) => {
    addReference(activeSessionId, message);
    requestInputFocus();
  }, [addReference, activeSessionId, requestInputFocus]);
  const startDiscussion = useCallback((message: ChatMessage, trigger: HTMLElement) => {
    returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : trigger;
    setSelected(message);
  }, []);
  return {
    onQuoteMessage: c.isSessionArchived ? undefined : quoteMessage,
    onStartDiscussion: canDiscuss ? startDiscussion : undefined,
    dialog: <Dialog open={!!discussion} onOpenChange={(open) => { if (!open) setSelected(null); }}>
      <DialogContent finalFocus={returnFocus}>
        <DialogHeader>
          <DialogTitle>{t(($) => $.message_actions.discussion_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.message_actions.discussion_description)}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose render={<Button variant="outline" />}>{t(($) => $.doc_mode.cancel)}</DialogClose>
          <Button disabled={!discussion || !canDiscuss} onClick={() => {
            if (!discussion || !c.activeAgent) return;
            addReference(null, discussion);
            startNewChat(c.activeAgent);
            setSelected(null);
          }}>{t(($) => $.message_actions.create_draft)}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>,
  };
}
