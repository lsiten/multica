"use client";

import { useChatReferenceDraftStore, EMPTY_CHAT_REFERENCES, useDocModeStore, type ChatReference, type DocumentReferenceInput, type SentMessageReference } from "@multica/core/chat";
import { createSafeId } from "@multica/core/utils";
import { useT } from "../../i18n";

export function useChatReferences(workspaceId: string, sessionId: string | null, onAdded?: () => void, onOpenMessage?: (reference: SentMessageReference) => void) {
  const { t } = useT("chat");
  const scope = JSON.stringify([workspaceId, sessionId]);
  const references = useChatReferenceDraftStore((state) => state.byScope[scope] ?? EMPTY_CHAT_REFERENCES);
  return {
    addReference: (input: DocumentReferenceInput) => {
      if (!sessionId) return;
      useChatReferenceDraftStore.getState().add(scope, {
        id: createSafeId(), label: input.resource.label ?? input.resource.relativePath ?? t(($) => $.doc_mode.issue), content: input.content,
        source: { workspaceId, resource: input.resource, revision: input.revision, state: input.state, extent: input.extent },
      });
      onAdded?.();
    },
    inputProps: {
      references,
      onOpenReference: (reference: ChatReference) => {
        if (reference.messageSource?.workspaceId === workspaceId) onOpenMessage?.({ label: reference.label, ...reference.messageSource });
        if (reference.source && sessionId) useDocModeStore.getState().openDoc(sessionId, reference.source.resource);
      },
      onRemoveReference: (id: string) => useChatReferenceDraftStore.getState().remove(scope, [id]),
      onReferencesConsumed: (ids: readonly string[]) => useChatReferenceDraftStore.getState().remove(scope, ids),
    },
  };
}
