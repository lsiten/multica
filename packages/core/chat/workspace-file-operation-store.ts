"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { z } from "zod";
import { defaultStorage } from "../platform/storage";
import { registerDraftCleanup } from "../drafts/cleanup-registry";
import { createSafeId } from "../utils";
import { workspaceFileOperationSchema, type WorkspaceFileOperation } from "../types/workspace-file-operation";

const ContextSchema = z.object({
  workspace_id: z.string().min(1), runtime_id: z.string().min(1),
  task_id: z.string().min(1), path: z.string().min(1),
});
const PendingSchema = z.object({ context: ContextSchema, commandId: z.string().min(1).max(128), operation: workspaceFileOperationSchema });
const PersistedSchema = z.object({ pending: z.record(z.string(), z.unknown()) });
export type WorkspaceFileOperationContext = Readonly<z.infer<typeof ContextSchema>>;
export type PendingWorkspaceFileOperation = Readonly<z.infer<typeof PendingSchema>>;

export function workspaceFileOperationScope(context: WorkspaceFileOperationContext): string {
  // A relative path is only meaningful inside its task/runtime checkout.
  // Include both identities so identical paths in parallel worktrees cannot
  // overwrite one another's pending command.
  return JSON.stringify([context.workspace_id, context.runtime_id, context.task_id, context.path]);
}

function legacyWorkspaceFileOperationScope(context: WorkspaceFileOperationContext): string {
  return JSON.stringify([context.workspace_id, context.path]);
}

interface WorkspaceFileOperationState {
  readonly pending: Readonly<Record<string, PendingWorkspaceFileOperation>>;
  readonly prepare: (context: WorkspaceFileOperationContext, operation: WorkspaceFileOperation) => PendingWorkspaceFileOperation;
  readonly settle: (scope: string, commandId: string) => void;
}

// A lost HTTP response must retry the original command and payload, including
// after a reload. Only a matching receipt or an explicit no-effect refusal ends it.
export const useWorkspaceFileOperationStore = create<WorkspaceFileOperationState>()(persist(
  (set, get) => ({
    pending: {},
    prepare: (context, operation) => {
      const scope = workspaceFileOperationScope(context);
      const existing = get().pending[scope];
      if (existing) {
        if (existing.context.runtime_id === context.runtime_id && existing.context.task_id === context.task_id) return existing;
        const rebound = { ...existing, context };
        set((state) => ({ pending: { ...state.pending, [scope]: rebound } }));
        return rebound;
      }
      const pending = { context, operation, commandId: createSafeId() };
      set((state) => ({ pending: { ...state.pending, [scope]: pending } }));
      return pending;
    },
    settle: (scope, commandId) => set((state) => {
      if (state.pending[scope]?.commandId !== commandId) return state;
      const pending = { ...state.pending };
      delete pending[scope];
      return { pending };
    }),
  }),
  {
    name: "multica_workspace_file_creations",
    storage: createJSONStorage(() => defaultStorage),
    partialize: (state) => ({ pending: state.pending }),
    merge: (persisted, current) => {
      const parsed = PersistedSchema.safeParse(persisted);
      if (!parsed.success) return current;
      const pending = Object.fromEntries(Object.entries(parsed.data.pending).flatMap(([scope, value]) => {
        const entry = PendingSchema.safeParse(value);
        if (!entry.success) return [];
        const expected = workspaceFileOperationScope(entry.data.context);
        const legacy = legacyWorkspaceFileOperationScope(entry.data.context);
        return scope === expected || scope === legacy ? [[expected, entry.data]] : [];
      }));
      return { ...current, pending };
    },
  },
));

registerDraftCleanup({
  storageKey: "multica_workspace_file_creations", workspaceScoped: false,
  resetInMemory: () => useWorkspaceFileOperationStore.setState({ pending: {} }),
});
