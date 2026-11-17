export { createChatStore, CHAT_MIN_W, CHAT_MIN_H, CHAT_DEFAULT_W, CHAT_DEFAULT_H, DRAFT_NEW_SESSION } from "./store";
export type { ChatStoreOptions, ChatState, ChatTimelineItem } from "./store";
export { useRecentContextStore, selectRecentContexts } from "./recent-context-store";
export {
  useDocModeStore,
  selectActiveDocument,
  selectWorking,
  selectDiffView,
  selectDocumentDraft,
  documentDraftKey,
} from "./doc-mode-store";
export type { DocumentDraft, DocumentKind, DocumentRef, DocumentTextLocation } from "./doc-mode-store";
export { useLocalDirectoryStore, selectCurrentLocalDirectory } from "./local-directory-store";
export type { CurrentLocalDirectory } from "./local-directory-store";
export { chatFileDirectories } from "./file-directories";
export type { ChatFileDirectory } from "./file-directories";
export { useWorkspaceFileOperationStore, workspaceFileOperationScope } from "./workspace-file-operation-store";
export type { PendingWorkspaceFileOperation, WorkspaceFileOperationContext } from "./workspace-file-operation-store";
export type { RecentContextEntry, RecentContextType } from "./recent-context-store";
export { canContinueChatFailure } from "./failure-recovery";
export { useChatRunPolicyStore } from "./run-policy-store";
export { useContinueChatTask, useCancelChatTask, useChatTaskStopping } from "./task-action-mutations";
export { useChatWorkspaceLayoutStore, DEFAULT_CHAT_WORKSPACE_LAYOUT } from "./workspace-layout-store";
export { useDocumentCurrentContent } from "./document-current-content";
export { CHAT_SESSION_TAB_MIME, CHAT_SESSION_TAB_PROMOTE_EVENT, DOCUMENT_TAB_MIME, chatSessionTabDragValue, documentTabDragSessionId, documentTabDragValue } from "./document-tab-drag";
export { composeChatReferences, readSentDocumentReferences, readSentMessageReferences, type SentMessageReference, type SentDocumentReference, type ChatReference, type DocumentReferenceInput } from "./document-reference";
export { useChatReferenceDraftStore, EMPTY_CHAT_REFERENCES } from "./reference-draft-store";
export { useChatMessageSearch } from "./message-search";
export { useDocumentComments, useCreateDocumentComment, useResolveDocumentComment } from "./document-comment-queries";
export type { DocumentComment, DocumentCommentAnchor } from "../api/document-comments";
export { documentCommentScopeKey } from "./document-comment-identity";
export { adoptWorkspaceFileDocumentIdentity, applyWorkspaceFileOperationResult, rebindWorkspaceFileDocument, updateWorkspaceFileDocumentLocation } from "./workspace-file-document-state";
export { useWorkspaceFileEditStore } from "./workspace-file-edit-store";
export { useChatReadingPositionStore, type ChatReadingPosition } from "./reading-position-store";
export { legacyDocumentDraftKey } from "./document-identity";
export { useDocumentCommentDraftStore, EMPTY_DOCUMENT_COMMENT_DRAFT } from "./document-comment-draft-store";

import type { createChatStore as CreateChatStoreFn } from "./store";

type ChatStoreInstance = ReturnType<typeof CreateChatStoreFn>;

/** Module-level singleton — set once at app boot via `registerChatStore()`. */
let _store: ChatStoreInstance | null = null;

/**
 * Register the chat store instance created by the app.
 * Must be called at boot before any component renders.
 */
export function registerChatStore(store: ChatStoreInstance) {
  _store = store;
}

/**
 * Singleton accessor — a Zustand hook backed by the registered instance.
 * Supports `useChatStore(selector)` and `useChatStore.getState()`.
 */
export const useChatStore: ChatStoreInstance = new Proxy(
  (() => {}) as unknown as ChatStoreInstance,
  {
    apply(_target, _thisArg, args) {
      if (!_store)
        throw new Error(
          "Chat store not initialised — call registerChatStore() first",
        );
      return (_store as unknown as (...a: unknown[]) => unknown)(...args);
    },
    get(_target, prop) {
      if (!_store) return undefined;
      return Reflect.get(_store, prop);
    },
  },
);
