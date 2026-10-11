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
export type DocumentKind = "issue" | "attachment";

export interface DocumentRef {
  kind: DocumentKind;
  id: string;
  // Optional display metadata carried with the document.
  label?: string;
  contentType?: string;
}

interface DocModeState {
  // The document the chat is currently in doc mode about, per workspace.
  // Null when no document is open. Persisted so a reload restores doc mode.
  activeByWorkspace: Record<string, DocumentRef | null>;
  // The floating "working" indicator (the agent is editing the document).
  workingByWorkspace: Record<string, boolean>;
  // The diff view toggle (D6: not shown by default; the user switches to it).
  diffViewByWorkspace: Record<string, boolean>;
  // Enter doc mode with a document (full-screen doc + floating working).
  openDoc: (wsId: string, doc: DocumentRef) => void;
  // Explicitly close the document (D9: only an explicit close returns to chat).
  closeDoc: (wsId: string) => void;
  // Set the floating "working" state; an edit completing must NOT clear doc mode.
  setWorking: (wsId: string, working: boolean) => void;
  // Toggle the diff view (D6).
  setDiffView: (wsId: string, diff: boolean) => void;
}

export const useDocModeStore = create<DocModeState>()(
  persist(
    (set) => ({
      activeByWorkspace: {},
      workingByWorkspace: {},
      diffViewByWorkspace: {},
      openDoc: (wsId, doc) =>
        set((state) => ({
          activeByWorkspace: { ...state.activeByWorkspace, [wsId]: doc },
          // Opening a document is an explicit action; keep the working flag
          // independent so a prior edit's "working" state does not leak in.
          workingByWorkspace: { ...state.workingByWorkspace, [wsId]: false },
        })),
      closeDoc: (wsId) =>
        set((state) => {
          if (
            !state.activeByWorkspace[wsId] &&
            state.workingByWorkspace[wsId] !== true
          )
            return state;
          return {
            activeByWorkspace: {
              ...state.activeByWorkspace,
              [wsId]: null,
            },
            workingByWorkspace: {
              ...state.workingByWorkspace,
              [wsId]: false,
            },
          };
        }),
      setWorking: (wsId, working) =>
        set((state) => ({
          workingByWorkspace: {
            ...state.workingByWorkspace,
            [wsId]: working,
          },
        })),
      setDiffView: (wsId, diff) =>
        set((state) => ({
          diffViewByWorkspace: {
            ...state.diffViewByWorkspace,
            [wsId]: diff,
          },
        })),
    }),
    {
      name: "multica_doc_mode",
      storage: createJSONStorage(() => defaultStorage),
      partialize: (state) => ({
        activeByWorkspace: state.activeByWorkspace,
        // "working" and "diff view" are ephemeral UI state, not persisted.
      }),
      version: 1,
      migrate: () => ({ activeByWorkspace: {} }),
    },
  ),
);

const NO_DOC: DocumentRef | null = null;

export function selectActiveDocument(wsId: string | null) {
  return (state: DocModeState) =>
    wsId ? (state.activeByWorkspace[wsId] ?? NO_DOC) : NO_DOC;
}

export function selectWorking(wsId: string | null) {
  return (state: DocModeState) => (wsId ? state.workingByWorkspace[wsId] ?? false : false);
}

export function selectDiffView(wsId: string | null) {
  return (state: DocModeState) => (wsId ? state.diffViewByWorkspace[wsId] ?? false : false);
}
