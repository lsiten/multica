"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { defaultStorage } from "../platform/storage";
import { registerDraftCleanup } from "../drafts/cleanup-registry";
import { restoreDocModeState } from "./doc-mode-persistence";
import { documentDraftKey } from "./document-identity";

export { documentDraftKey } from "./document-identity";

// Selected documents and drafts belong to their conversation. The workspace
// layout decides whether the document is beside chat or focused; finishing an
// edit never closes it. Server content and versions remain in TanStack Query.
export type DocumentKind = "issue" | "attachment" | "workspace_file";

export interface DocumentTextLocation {
  readonly offset: number;
  readonly text: string;
  readonly line: number;
}

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
  projectResourceId?: string;
  runtimeId?: string;
  workDir?: string;
  relativePath?: string;
  /** Server-owned file identity remains independent of its current location. */
  readonly fileId?: string;
  readonly directoryId?: string;
  readonly directoryRevision?: number;
}

export type DocumentDraft =
  | {
      readonly kind: "version";
      readonly content: string;
      readonly baseContent: string;
      readonly baseRevision: number;
      readonly contentType: string;
    }
  | {
      readonly kind: "workspace_file";
      readonly content: string;
      readonly baseContent: string;
    };

interface DocModeState {
  // The document the chat is currently in doc mode about, per session.
  // Null when no document is open. Persisted so a reload restores doc mode.
  // Keyed by session (not workspace) so each session keeps its own open
  // document: switching sessions shows that session's document, and switching
  // back restores it, instead of leaking one session's document across another.
  activeBySession: Record<string, DocumentRef | null>;
  openBySession: Record<string, readonly DocumentRef[]>;
  // The floating "working" indicator (the agent is editing the document).
  workingBySession: Record<string, boolean>;
  // The diff view toggle (D6: not shown by default; the user switches to it).
  diffViewBySession: Record<string, boolean>;
  // Unsaved edits survive closing, switching documents, reloads and session
  // changes. The key is derived from the document identity, not the session.
  draftsByDocument: Record<string, DocumentDraft>;
  locationBySession: Record<string, (DocumentTextLocation & { readonly documentKey: string }) | undefined>;
  // Enter doc mode with a document (full-screen doc + floating working).
  openDoc: (sessionId: string, doc: DocumentRef, location?: DocumentTextLocation) => void;
  clearLocation: (sessionId: string) => void;
  // Explicitly close the document (D9: only an explicit close returns to chat).
  closeDoc: (sessionId: string) => void;
  selectChat: (sessionId: string) => void;
  closeTab: (sessionId: string, key: string) => void;
  moveTab: (sessionId: string, key: string, target: string) => void;
  // Set the floating "working" state; an edit completing must NOT clear doc mode.
  setWorking: (sessionId: string, working: boolean) => void;
  // Toggle the diff view (D6).
  setDiffView: (sessionId: string, diff: boolean) => void;
  setDocumentDraft: (doc: DocumentRef, draft: DocumentDraft) => void;
  clearDocumentDraft: (doc: DocumentRef) => void;
}

export const useDocModeStore = create<DocModeState>()(
  persist(
    (set) => ({
      activeBySession: {},
      openBySession: {},
      workingBySession: {},
      diffViewBySession: {},
      draftsByDocument: {},
      locationBySession: {},
      clearLocation: (sessionId) => set((state) => ({ locationBySession: { ...state.locationBySession, [sessionId]: undefined } })),
      openDoc: (sessionId, doc, location) =>
        set((state) => {
          const opened = state.openBySession[sessionId] ?? [];
          const key = documentDraftKey(doc);
          const existing = opened.find((entry) => documentDraftKey(entry) === key);
          const retainedLocation = existing && (existing.directoryRevision ?? -1) > (doc.directoryRevision ?? -1)
            ? { relativePath: existing.relativePath, label: existing.label, directoryId: existing.directoryId, directoryRevision: existing.directoryRevision }
            : {};
          const selected = doc.kind === "workspace_file"
            ? { ...doc, ...retainedLocation, id: existing?.id ?? doc.fileId ?? doc.id }
            : doc;
          return {
          locationBySession: { ...state.locationBySession, [sessionId]: location ? { ...location, documentKey: key } : undefined },
          activeBySession: { ...state.activeBySession, [sessionId]: selected },
          openBySession: {
            ...state.openBySession,
            [sessionId]: opened.some((entry) => documentDraftKey(entry) === key)
              ? opened.map((entry) => documentDraftKey(entry) === key ? selected : entry)
              : [...opened, selected],
          },
          // Opening a document is an explicit action; keep the working flag
          // independent so a prior edit's "working" state does not leak in.
          workingBySession: { ...state.workingBySession, [sessionId]: false },
          };
        }),
      closeDoc: (sessionId) =>
        set((state) => {
          const active = state.activeBySession[sessionId];
          if (
            !state.activeBySession[sessionId] &&
            state.workingBySession[sessionId] !== true
          )
            return state;
          return {
            openBySession: {
              ...state.openBySession,
              [sessionId]: active
                ? (state.openBySession[sessionId] ?? []).filter((opened) => documentDraftKey(opened) !== documentDraftKey(active))
                : state.openBySession[sessionId] ?? [],
            },
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
      selectChat: (sessionId) => set((state) => ({ activeBySession: { ...state.activeBySession, [sessionId]: null } })),
      closeTab: (sessionId, key) => set((state) => {
        const remaining = (state.openBySession[sessionId] ?? []).filter((doc) => documentDraftKey(doc) !== key);
        const active = state.activeBySession[sessionId];
        return {
          openBySession: { ...state.openBySession, [sessionId]: remaining },
          activeBySession: { ...state.activeBySession, [sessionId]: active && documentDraftKey(active) === key ? remaining.at(-1) ?? null : active ?? null },
        };
      }),
      moveTab: (sessionId, key, target) => set((state) => {
        const opened = state.openBySession[sessionId] ?? [];
        const moving = opened.find((doc) => documentDraftKey(doc) === key);
        if (!moving || key === target) return state;
        const others = opened.filter((doc) => documentDraftKey(doc) !== key);
        const index = others.findIndex((doc) => documentDraftKey(doc) === target);
        if (index < 0) return state;
        return { openBySession: { ...state.openBySession, [sessionId]: [...others.slice(0, index), moving, ...others.slice(index)] } };
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
      setDocumentDraft: (doc, draft) =>
        set((state) => ({
          draftsByDocument: {
            ...state.draftsByDocument,
            [documentDraftKey(doc)]: draft,
          },
        })),
      clearDocumentDraft: (doc) =>
        set((state) => {
          const key = documentDraftKey(doc);
          if (!state.draftsByDocument[key]) return state;
          const { [key]: _removed, ...draftsByDocument } = state.draftsByDocument;
          return { draftsByDocument };
        }),
    }),
    {
      name: "multica_doc_mode",
      storage: createJSONStorage(() => defaultStorage),
      partialize: (state) => ({
        activeBySession: state.activeBySession,
        openBySession: state.openBySession,
        draftsByDocument: state.draftsByDocument,
        // "working" and "diff view" are ephemeral UI state, not persisted.
      }),
      version: 4,
      migrate: (persisted, version) => {
        // v1 persisted by workspace; drop it so no stale workspace-scoped
        // document leaks across sessions once the key becomes session-scoped.
        if (version < 2) return restoreDocModeState({});
        return restoreDocModeState(persisted);
      },
      merge: (persisted, current) => ({ ...current, ...restoreDocModeState(persisted) }),
    },
  ),
);

const NO_DOC: DocumentRef | null = null;

registerDraftCleanup({
  storageKey: "multica_doc_mode", workspaceScoped: false,
  resetInMemory: () => useDocModeStore.setState({ activeBySession: {}, openBySession: {}, draftsByDocument: {}, workingBySession: {}, diffViewBySession: {}, locationBySession: {} }),
});

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

export function selectDocumentDraft(doc: DocumentRef | null) {
  return (state: DocModeState) => (doc ? state.draftsByDocument[documentDraftKey(doc)] : undefined);
}
