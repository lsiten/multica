import { useMutation } from "@tanstack/react-query";
import { create } from "zustand";
import { api } from "../api";
import type {
  ApplyRuntimeFileEditRequest,
  RuntimeFileEditResult,
} from "../api/runtime-file-edit";

// Runtime file editing — the client data layer for a human or agent edit to a
// runtime working file. Mirrors document-content-version's "content version +
// optimistic lock" contract on top of api.applyRuntimeFileEdit:
//
//   - "edit"       compare-and-swap write; a changed base yields an ApiError(409)
//                  so the caller reloads (or rebases) against the current content.
//   - "merge_edit" three-way merge; an unresolvable conflict returns 200 with
//                  conflict: true (the runtime version won) — the caller rebases.
//
// git operations are never local-first; binary files are read-only and never
// reach this layer.

export const runtimeFileEditKeys = {
  all: (wsId: string) => ["runtime-file-edit", wsId] as const,
  file: (wsId: string, taskId: string, path: string) =>
    [...runtimeFileEditKeys.all(wsId), taskId, path] as const,
};

// Apply an edit to a runtime working file. The mutation function returns the
// applied result (edit digest or merge outcome); a changed base throws ApiError
// (409) and a merge conflict resolves to { conflict: true }, so both failure
// modes are surfaced to the caller instead of silently swallowed.
export function useApplyRuntimeFileEdit() {
  return useMutation({
    mutationFn: (request: ApplyRuntimeFileEditRequest): Promise<RuntimeFileEditResult | null> =>
      api.applyRuntimeFileEdit(request),
  });
}

// Whether the editor's new content differs from the base it was opened on —
// the gate for enabling Save / dirty state. A byte-inequality is enough: the
// daemon re-merges from (base, new) regardless of how the text changed.
export function hasRuntimeFileEdit(base: string, edited: string): boolean {
  return base !== edited;
}

// Ephemeral per-file client draft for the remote path. It is intentionally NOT
// persisted to storage: a dropped draft is an accepted tradeoff (the runtime is
// the source of truth), and only the in-flight edit lives here. The local path
// writes directly and needs no draft.
export interface RuntimeFileEditDraft {
  // The content the editor opened; the optimistic-lock base.
  base: string;
  // Base digest (local compare-and-swap) — set when the file was opened.
  baseSha256: string;
  // The current edited content.
  edited: string;
}

interface RuntimeFileEditDraftState {
  drafts: Record<string, RuntimeFileEditDraft>;
  key: (taskId: string, path: string) => string;
  open: (taskId: string, path: string, draft: RuntimeFileEditDraft) => void;
  setEdited: (taskId: string, path: string, edited: string) => void;
  discard: (taskId: string, path: string) => void;
}

// The remote client draft lives in a Zustand store, never in storage: it holds
// only an in-flight edit, so a reload drops it (accepted tradeoff).
export const useRuntimeFileEditDraftStore = create<RuntimeFileEditDraftState>()(
  (set) => ({
    drafts: {},
    key: (taskId, path) => `${taskId}\u0000${path}`,
    open: (taskId, path, draft) =>
      set((state) => ({
        drafts: { ...state.drafts, [state.key(taskId, path)]: draft },
      })),
    setEdited: (taskId, path, edited) =>
      set((state) => {
        const id = state.key(taskId, path);
        const current = state.drafts[id];
        if (!current) return state;
        return { drafts: { ...state.drafts, [id]: { ...current, edited } } };
      }),
    discard: (taskId, path) =>
      set((state) => {
        const id = state.key(taskId, path);
        const drafts = { ...state.drafts };
        delete drafts[id];
        return { drafts };
      }),
  }),
);
