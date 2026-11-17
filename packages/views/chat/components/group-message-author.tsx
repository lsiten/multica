"use client";

import type { ChatMessage } from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";
import { useActorName } from "@multica/core/workspace/hooks";
import { useT } from "../../i18n";

/**
 * Names the sender of a group-chat message. Direct (one-to-one) chats carry no
 * author (`author_id` is null), so this renders nothing there — which keeps
 * ordinary chats unchanged. A member's own messages are anchored to the right
 * and need no label; every other participant's message is labelled above the
 * bubble so a group conversation stays attributable.
 */
export function GroupMessageAuthor({ message }: { message: ChatMessage }) {
  const { getActorName } = useActorName();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const { t } = useT("chat");

  if (!message.author_id || !message.author_type) return null;
  // Your own messages sit at the right edge; no name is needed to tell them
  // apart from others'.
  if (message.author_type === "member" && message.author_id === currentUserId) {
    return null;
  }
  const name = getActorName(message.author_type, message.author_id);
  const role = message.author_type === "agent"
    ? t(($) => $.doc_mode.agent_author)
    : t(($) => $.doc_mode.member_author);
  return (
    <div className="mb-1 flex items-center gap-1 text-micro text-muted-foreground" aria-label={`${name} · ${role}`}>
      <span>{name}</span>
      <span aria-hidden="true">· {role}</span>
    </div>
  );
}
