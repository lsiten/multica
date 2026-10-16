"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { defaultStorage } from "../platform/storage";

// Doc mode is the confirmed v12 interaction: opening a project document fills the
// main area, the chat stays in its original conversation (D10), a floating
// layer shows only a "working" status (D7), and an edit completes WITHOUT
// auto-switching back to the chat (D9). Only an explicit close returns to the
// chat. This store holds the client-only doc-mode state that gates that UI;
// server data (the document content / versions) stays in TanStack Query.
export type DocumentKind = "issue" | "attachment" | "workspace_file";

export interface DocumentRef {
  kind: DocumentKind;
  id: string;
  // Optional display metadata carried with the document.
  label?: string;
  contentType?: string;
  /** Workspace-file refs resolve their absolute path through the session API. */
  sessionId?: string;
  projectId?: string;
  taskId?: string;
  runtimeId?: string;
  workDir?: string;
  relativePath?: string;
}

interface DocModeState {
  // The document the chat is currently in doc mode about, per session.
  // Null when no document is open. Persisted so a reload restores doc mode.
  // Keyed by session (not workspace) so each session keeps its own open
  // document: switching sessions shows that session's document, and switching
  // back restores it, instead of leaking one session's document across another.
  activeBySession: Record<string, DocumentRef | null>;
  // The floating "working" indicator (the agent is editing the document).
  workingBySession: Record<string, boolean>;
  // The diff view toggle (D6: not shown by default; the user switches to it).
  diffViewBySession: Record<string, boolean>;
  // Enter doc mode with a document (full-screen doc + floating working).
  openDoc: (sessionId: string, doc: DocumentRef) => void;
  // Explicitly close the document (D9: only an explicit close returns to chat).
  closeDoc: (sessionId: string) => void;
  // Set the floating "working" state; an edit completing must NOT clear doc mode.
  setWorking: (sessionId: string, working: boolean) => void;
  // Toggle the diff view (D6).
  setDiffView: (sessionId: string, diff: boolean) => void;
}

export const useDocModeStore = create<DocModeState>()(
  persist(
    (set) => ({
      activeBySession: {},
      workingBySession: {},
      diffViewBySession: {},
      openDoc: (sessionId, doc) =>
        set((state) => ({
          activeBySession: { ...state.activeBySession, [sessionId]: doc },
          // Opening a document is an explicit action; keep the working flag
          // independent so a prior edit's "working" state does not leak in.
          workingBySession: { ...state.workingBySession, [sessionId]: false },
        })),
      closeDoc: (sessionId) =>
        set((state) => {
          if (
            !state.activeBySession[sessionId] &&
            state.workingBySession[sessionId] !== true
          )
            return state;
          return {
            activeBySession: {
              ...state.activeBySession,
              [sessionId]: null,
            },
            workingBySession: {
              ...state.workingBySession,
              [sessionId]: false,
            },
          };
        }),
      setWorking: (sessionId, working) =>
        set((state) => ({
          workingBySession: {
            ...state.workingBySession,
            [sessionId]: working,
          },
        })),
      setDiffView: (sessionId, diff) =>
        set((state) => ({
          diffViewBySession: {
            ...state.diffViewBySession,
            [sessionId]: diff,
          },
        })),
    }),
    {
      name: "multica_doc_mode",
      storage: createJSONStorage(() => defaultStorage),
      partialize: (state) => ({
        activeBySession: state.activeBySession,
        // "working" and "diff view" are ephemeral UI state, not persisted.
      }),
      version: 2,
      // v1 persisted by workspace; drop it so no stale workspace-scoped document
      // leaks across sessions once the key becomes session-scoped.
      migrate: () => ({ activeBySession: {} }),
    },
  ),
);

const NO_DOC: DocumentRef | null = null;

export function selectActiveDocument(sessionId: string | null) {
  return (state: DocModeState) =>
    sessionId ? (state.activeBySession[sessionId] ?? NO_DOC) : NO_DOC;
}

export function selectWorking(sessionId: string | null) {
  return (state: DocModeState) => (sessionId ? state.workingBySession[sessionId] ?? false : false);
}

export function selectDiffView(sessionId: string | null) {
  return (state: DocModeState) => (sessionId ? state.diffViewBySession[sessionId] ?? false : false);
}
