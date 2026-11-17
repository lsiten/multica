"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { z } from "zod";
import { DocumentCommentAnchorSchema } from "../api/document-comments";
import { registerDraftCleanup } from "../drafts/cleanup-registry";
import { defaultStorage } from "../platform/storage";
import { createSafeId } from "../utils";

const DraftSchema = z.object({ content: z.string().max(16000), anchor: DocumentCommentAnchorSchema.optional(), submissionId: z.string().uuid().optional() });
const PersistedSchema = z.object({ drafts: z.record(z.string(), z.unknown()) });
export type DocumentCommentDraft = Readonly<z.infer<typeof DraftSchema>>;
export const EMPTY_DOCUMENT_COMMENT_DRAFT: DocumentCommentDraft = { content: "" };

interface DocumentCommentDraftState {
  readonly drafts: Readonly<Record<string, DocumentCommentDraft>>;
  readonly setDraft: (scope: string, draft: DocumentCommentDraft) => void;
  readonly clearDraft: (scope: string, sent: DocumentCommentDraft) => void;
}

export const useDocumentCommentDraftStore = create<DocumentCommentDraftState>()(persist(
  (set) => ({
    drafts: {},
    setDraft: (scope, draft) => set((state) => ({ drafts: { ...state.drafts, [scope]: { ...draft, submissionId: draft.submissionId ?? createSafeId() } } })),
    clearDraft: (scope, sent) => set((state) => {
      if (state.drafts[scope] !== sent) return state;
      const drafts = { ...state.drafts };
      delete drafts[scope];
      return { drafts };
    }),
  }),
  {
    name: "multica_document_comment_drafts",
    storage: createJSONStorage(() => defaultStorage),
    partialize: (state) => ({ drafts: state.drafts }),
    merge: (persisted, current) => {
      const parsed = PersistedSchema.safeParse(persisted);
      if (!parsed.success) return current;
      const drafts = Object.fromEntries(Object.entries(parsed.data.drafts).flatMap(([key, value]) => {
        const draft = DraftSchema.safeParse(value);
        return draft.success ? [[key, draft.data] as const] : [];
      }));
      return { ...current, drafts };
    },
  },
));

registerDraftCleanup({
  storageKey: "multica_document_comment_drafts", workspaceScoped: false,
  resetInMemory: () => useDocumentCommentDraftStore.setState({ drafts: {} }),
});
