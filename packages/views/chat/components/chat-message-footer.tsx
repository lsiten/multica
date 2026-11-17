"use client";

import { toast } from "sonner";
import { Copy } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import type { ChatMessage } from "@multica/core/types";
import type { ChatTimelineItem } from "@multica/core/chat";
import { extractCopyText } from "../lib/copy-text";
import { formatElapsedMs } from "../lib/format";
import { useT } from "../../i18n";
import { ChatMessageActions, type ChatMessageActionCallbacks } from "./chat-message-actions";

export function ChatMessageFooter({ message, timeline, isPending, transformContent, onQuoteMessage, onStartDiscussion }: ChatMessageActionCallbacks & {
  readonly message: ChatMessage;
  readonly timeline: ChatTimelineItem[];
  readonly isPending: boolean;
  readonly transformContent?: (content: string) => string;
}) {
  const isNoResponse = message.message_kind === "no_response";
  const copyContent = extractCopyText(message, timeline, transformContent);
  const showCopy = !isPending && !isNoResponse && copyContent.trim().length > 0;
  const showActions = !isPending && !isNoResponse && (!!onQuoteMessage || !!onStartDiscussion);
  if (message.elapsed_ms == null && !showCopy && !showActions) return null;
  return <div className="flex flex-wrap items-center gap-1.5">
    {message.elapsed_ms != null && <ElapsedCaption variant={isNoResponse ? "finished" : "replied"} elapsedMs={message.elapsed_ms} />}
    {showCopy && <MessageCopyButton content={copyContent} />}
    {showActions && <ChatMessageActions message={message} onQuoteMessage={onQuoteMessage} onStartDiscussion={onStartDiscussion} inline />}
  </div>;
}

function MessageCopyButton({ content }: { readonly content: string }) {
  const { t } = useT("chat");
  return <Tooltip>
    <TooltipTrigger render={<Button variant="ghost" size="icon-xs" className="text-faint-foreground hover:text-foreground"
      aria-label={t(($) => $.message_list.copy_action)} onClick={() => void copyText(content).then((copied) => {
        if (copied) toast.success(t(($) => $.message_list.copied_toast));
        else toast.error(t(($) => $.message_list.copy_failed_toast));
      })} />}><Copy /></TooltipTrigger>
    <TooltipContent side="top">{t(($) => $.message_list.copy_action)}</TooltipContent>
  </Tooltip>;
}

export function ElapsedCaption({ variant, elapsedMs, className }: {
  readonly variant: "replied" | "failed" | "finished";
  readonly elapsedMs: number;
  readonly className?: string;
}) {
  const { t } = useT("chat");
  const elapsed = formatElapsedMs(elapsedMs);
  let text: string;
  switch (variant) {
    case "replied": text = t(($) => $.message_list.replied_in, { elapsed }); break;
    case "finished": text = t(($) => $.message_list.finished_in, { elapsed }); break;
    case "failed": text = t(($) => $.message_list.failed_after, { elapsed }); break;
    default: { const unhandled: never = variant; return unhandled; }
  }
  return <div className={cn("text-caption text-muted-foreground", className)}>{text}</div>;
}
