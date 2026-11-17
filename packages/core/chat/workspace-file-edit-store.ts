"use client";

import { z } from "zod";
import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { defaultStorage } from "../platform/storage";
import { registerDraftCleanup } from "../drafts/cleanup-registry";
import { createSafeId } from "../utils";
import { ApplyRuntimeFileEditRequestSchema, type ApplyRuntimeFileEditRequest } from "../api/runtime-file-edit";
import { documentDraftKey } from "./document-identity";
import type { DocumentRef } from "./doc-mode-store";

const PendingSchema = z.object({ documentKey: z.string().min(1), request: ApplyRuntimeFileEditRequestSchema });
const PersistedSchema = z.object({ pending: z.record(z.string(), z.unknown()) });
type PendingEdit = Readonly<z.infer<typeof PendingSchema>>;

function editScope(pending: PendingEdit): string {
  return JSON.stringify([pending.request.workspace_id ?? "", pending.documentKey]);
}

interface WorkspaceFileEditState {
  readonly pending: Readonly<Record<string, PendingEdit>>;
  readonly prepare: (document: DocumentRef, request: Omit<ApplyRuntimeFileEditRequest, "command_id">) => PendingEdit;
  readonly settle: (pending: PendingEdit) => void;
}

// Retrying an uncertain save must retain the submitted content and command,
// even when the editor has since accumulated a newer local draft.
export const useWorkspaceFileEditStore = create<WorkspaceFileEditState>()(persist(
  (set, get) => ({
    pending: {},
    prepare: (document, request) => {
      const documentKey = documentDraftKey(document);
      const scope = JSON.stringify([request.workspace_id ?? "", documentKey]);
      const previous = get().pending[scope];
      if (previous) return previous;
      const pending = { documentKey, request: { ...request, command_id: createSafeId() } };
      set((state) => ({ pending: { ...state.pending, [scope]: pending } }));
      return pending;
    },
    settle: (pending) => set((state) => {
      const scope = editScope(pending);
      if (state.pending[scope]?.request.command_id !== pending.request.command_id) return state;
      const remaining = { ...state.pending };
      delete remaining[scope];
      return { pending: remaining };
    }),
  }),
  {
    name: "multica_workspace_file_edits",
    storage: createJSONStorage(() => defaultStorage),
    partialize: (state) => ({ pending: state.pending }),
    merge: (persisted, current) => {
      const parsed = PersistedSchema.safeParse(persisted);
      if (!parsed.success) return current;
      const pending = Object.fromEntries(Object.entries(parsed.data.pending).flatMap(([scope, value]) => {
        const entry = PendingSchema.safeParse(value);
        return entry.success && editScope(entry.data) === scope ? [[scope, entry.data]] : [];
      }));
      return { ...current, pending };
    },
  },
));

registerDraftCleanup({ storageKey: "multica_workspace_file_edits", workspaceScoped: false, resetInMemory: () => useWorkspaceFileEditStore.setState({ pending: {} }) });
