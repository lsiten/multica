"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { z } from "zod";
import { defaultStorage } from "../platform/storage";
import { registerDraftCleanup } from "../drafts/cleanup-registry";
import { ChatReferenceSchema, type ChatReference } from "./document-reference";

const PersistedReferencesSchema = z.object({ byScope: z.record(z.string(), z.array(ChatReferenceSchema)) });
export const EMPTY_CHAT_REFERENCES: ChatReference[] = [];

interface ReferenceDraftState {
  readonly byScope: Readonly<Record<string, ChatReference[]>>;
  readonly add: (scope: string, reference: ChatReference) => void;
  readonly remove: (scope: string, ids: readonly string[]) => void;
}

export const useChatReferenceDraftStore = create<ReferenceDraftState>()(persist(
  (set) => ({
    byScope: {},
    add: (scope, reference) => set((state) => ({
      byScope: { ...state.byScope, [scope]: [...(state.byScope[scope] ?? []), reference] },
    })),
    remove: (scope, ids) => set((state) => ({
      byScope: { ...state.byScope, [scope]: (state.byScope[scope] ?? []).filter((reference) => !ids.includes(reference.id)) },
    })),
  }),
  {
    name: "multica_chat_reference_drafts",
    storage: createJSONStorage(() => defaultStorage),
    partialize: (state) => ({ byScope: state.byScope }),
    merge: (persisted, current) => {
      const parsed = PersistedReferencesSchema.safeParse(persisted);
      return parsed.success ? { ...current, byScope: parsed.data.byScope } : current;
    },
  },
));

registerDraftCleanup({
  storageKey: "multica_chat_reference_drafts", workspaceScoped: false,
  resetInMemory: () => useChatReferenceDraftStore.setState({ byScope: {} }),
});
