import { useCallback, useLayoutEffect, useRef } from "react";
import { toUploadResult } from "@multica/core/hooks/use-file-upload";
import type { DraftUpload } from "@multica/core/drafts";
import type { Attachment, ChatMessage } from "@multica/core/types";
import { attachmentMarkdown } from "../../editor/use-coordinated-uploads";
import type { InputHistoryDirection } from "../../editor/extensions/input-history";
import { escapeMarkdownLabel } from "../../editor/utils/escape-markdown-label";

interface HistoryDraft {
  readonly content: string;
  readonly attachments: Attachment[];
}

interface HistoryCursor {
  readonly messageId: string;
  readonly markdown: string;
  readonly attachmentIds: readonly string[];
}

export function useChatInputHistory(options: {
  readonly scope: string;
  readonly sessionId: string;
  readonly messages: readonly ChatMessage[];
}) {
  const cursor = useRef<HistoryCursor | null>(null);
  const reset = useCallback(() => { cursor.current = null; }, []);
  useLayoutEffect(reset, [options.scope, reset]);

  const navigate = (
    input: {
      readonly direction: InputHistoryDirection;
      readonly markdown: string;
      readonly empty: boolean;
      readonly uploads: readonly DraftUpload[];
    },
    apply: (draft: HistoryDraft) => string,
  ): boolean => {
    if (input.uploads.some((upload) => upload.status !== "uploaded")) return false;
    if (input.empty && input.uploads.length === 0) reset();

    const previous = cursor.current;
    if (previous) {
      if (
        input.markdown !== previous.markdown ||
        input.uploads.length !== previous.attachmentIds.length ||
        input.uploads.some((upload) => upload.status !== "uploaded" ||
          !previous.attachmentIds.includes(upload.attachment.id))
      ) {
        reset();
        return false;
      }
    } else if (!input.empty || input.uploads.length > 0) {
      return false;
    }

    const messages = options.messages.filter((message) =>
      message.chat_session_id === options.sessionId && message.role === "user" &&
      (!message.message_kind || message.message_kind === "message") &&
      (message.content.length > 0 || (message.attachments?.length ?? 0) > 0),
    );
    const index = previous
      ? messages.findIndex((message) => message.id === previous.messageId)
      : messages.length;
    if (previous && index < 0) {
      reset();
      return false;
    }
    const nextIndex = index + (input.direction === "previous" ? -1 : 1);
    if (nextIndex < 0) return previous !== null;
    if (!previous && input.direction === "next") return false;
    if (nextIndex >= messages.length) {
      apply({ content: "", attachments: [] });
      reset();
      return true;
    }
    const message = messages[nextIndex];
    if (!message) return false;
    // Historical attachments remain owned by their original message. Their
    // stable links can be reused, but their IDs must never be rebound on send.
    const attachments = (message.attachments ?? []).map((attachment) => ({
      ...attachment,
      chat_message_id: message.id,
    }));
    let content = message.content;
    for (const attachment of attachments) {
      const stableLink = toUploadResult(attachment).markdownLink;
      for (const url of [attachment.download_url, attachment.url]) {
        if (url && url !== stableLink) {
          content = content
            .replaceAll(`](${url})`, `](${stableLink})`)
            .replaceAll(`<${url}>`, `<${stableLink}>`);
        }
      }
      if (!content.includes(stableLink)) {
        const link = attachmentMarkdown({
          ...attachment,
          filename: escapeMarkdownLabel(attachment.filename),
        });
        content = [content, link].filter(Boolean).join("\n\n");
      }
    }
    const markdown = apply({ content, attachments });
    cursor.current = {
      messageId: message.id,
      markdown,
      attachmentIds: attachments.map((attachment) => attachment.id),
    };
    return true;
  };

  return { navigate, reset };
}
