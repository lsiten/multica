"use client";

import { toast } from "sonner";
import { Copy, MessageSquarePlus, ArrowUpRight } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { copyText } from "@multica/ui/lib/clipboard";
import type { ChatMessage } from "@multica/core/types";
import { useT } from "../../i18n";
import { canonicalAnswerText } from "../lib/copy-text";

export interface ChatMessageActionCallbacks {
  readonly onQuoteMessage?: (message: ChatMessage) => void;
  readonly onStartDiscussion?: (message: ChatMessage, trigger: HTMLElement) => void;
}

export function ChatMessageActions({ message, onQuoteMessage, onStartDiscussion, inline = false }: ChatMessageActionCallbacks & { readonly message: ChatMessage; readonly inline?: boolean }) {
  const { t } = useT("chat");
  const text = canonicalAnswerText(message);
  if ((!text.trim() && !message.attachments?.length) || message.message_kind === "no_response" || message.failure_reason) return null;
  if (message.role === "assistant" && !onQuoteMessage && !onStartDiscussion) return null;
  return <div className={inline ? "contents" : `mt-1 flex flex-wrap gap-1 ${message.role === "user" ? "justify-end" : ""}`}>
    {message.role === "user" && text.trim() && <Button variant="ghost" size="icon-xs" className="text-muted-foreground" aria-label={t(($) => $.message_list.copy_action)} onClick={() => void copyText(text).then((copied) => {
      if (copied) toast.success(t(($) => $.message_list.copied_toast));
      else toast.error(t(($) => $.message_list.copy_failed_toast));
    })}><Copy /></Button>}
    {onQuoteMessage && <Button variant="ghost" size="xs" className="text-muted-foreground" onClick={() => onQuoteMessage(message)}><MessageSquarePlus />{t(($) => $.message_actions.quote)}</Button>}
    {onStartDiscussion && <Button variant="ghost" size="xs" className="text-muted-foreground" onClick={(event) => onStartDiscussion(message, event.currentTarget)}><ArrowUpRight />{t(($) => $.message_actions.new_discussion)}</Button>}
  </div>;
}
