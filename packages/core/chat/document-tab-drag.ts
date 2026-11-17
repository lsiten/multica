import { documentDraftKey, type DocumentRef } from "./doc-mode-store";

export const DOCUMENT_TAB_MIME = "application/x-multica-document-tab";
export const CHAT_SESSION_TAB_MIME = "application/x-multica-chat-session-tab";
export const CHAT_SESSION_TAB_PROMOTE_EVENT = "multica:promote-chat-tab";

export function documentTabDragValue(sessionId: string, document: DocumentRef): string {
  return `${sessionId}\n${documentDraftKey(document)}`;
}

export function documentTabDragSessionId(value: string): string {
  return value.split("\n", 1)[0]?.trim() ?? "";
}

/** Payload used when a floating chat is dragged onto the desktop tab strip. */
export function chatSessionTabDragValue(sessionId: string): string {
  return sessionId;
}
