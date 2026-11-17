import { useDocModeStore, type DocumentRef } from "./doc-mode-store";
import { adoptDocumentDraftIdentity } from "./document-draft-identity";
import { documentDraftKey, legacyDocumentDraftKey } from "./document-identity";
import { useChatWorkspaceLayoutStore } from "./workspace-layout-store";
import type { WorkspaceFileOperationResult } from "../types/workspace-file-operation";

export function updateWorkspaceFileDocumentLocation(previous: DocumentRef, location: {
  readonly fileId: string;
  readonly directoryId?: string;
  readonly directoryRevision: number;
  readonly relativePath: string;
}): void {
  if (previous.kind !== "workspace_file" || previous.fileId !== location.fileId) return;
  const update = (ref: DocumentRef): DocumentRef => {
    if (ref.kind !== "workspace_file" || ref.fileId !== location.fileId
      || (ref.directoryRevision ?? -1) > location.directoryRevision) return ref;
    if (ref.relativePath === location.relativePath && ref.directoryId === location.directoryId
      && ref.directoryRevision === location.directoryRevision) return ref;
    return { ...ref, ...location, label: ref.label === ref.relativePath ? location.relativePath : ref.label };
  };
  useDocModeStore.setState((state) => ({
    openBySession: Object.fromEntries(Object.entries(state.openBySession).map(([session, refs]) => [session, refs.map(update)])),
    activeBySession: Object.fromEntries(Object.entries(state.activeBySession).map(([session, ref]) => [session, ref ? update(ref) : null])),
  }));
}

/** Apply a daemon file-operation receipt to every open document with the same
 * stable file identity. Drafts remain keyed by fileId, so a move does not
 * orphan an in-progress editor; trash closes the stale tab while preserving
 * that draft for a later restore. */
export function applyWorkspaceFileOperationResult(result: WorkspaceFileOperationResult): void {
  if (!result.file_id) return;
  const fileId = result.file_id;
  const nextPath = result.destination ?? result.path;
  const location = {
    relativePath: nextPath,
    ...(result.directory_id !== undefined ? { directoryId: result.directory_id } : {}),
    ...(result.directory_revision !== undefined ? { directoryRevision: result.directory_revision } : {}),
  };
  useDocModeStore.setState((state) => {
    const openBySession = Object.fromEntries(Object.entries(state.openBySession).map(([session, refs]) => {
      if (result.operation === "trash") return [session, refs.filter((ref) => ref.kind !== "workspace_file" || ref.fileId !== fileId)];
      return [session, refs.map((ref) => ref.kind === "workspace_file" && ref.fileId === fileId
        ? { ...ref, ...location, label: ref.label === ref.relativePath ? nextPath : ref.label }
        : ref)];
    }));
    const activeBySession = Object.fromEntries(Object.entries(state.activeBySession).map(([session, ref]) => {
      if (!ref || ref.kind !== "workspace_file" || ref.fileId !== fileId) return [session, ref];
      if (result.operation === "trash") return [session, null];
      return [session, { ...ref, ...location, label: ref.label === ref.relativePath ? nextPath : ref.label }];
    }));
    return { openBySession, activeBySession };
  });
}

export function rebindWorkspaceFileDocument(previous: DocumentRef, binding: {
  readonly runtimeId: string;
  readonly taskId?: string;
  readonly workDir?: string;
  readonly relativePath?: string;
}): void {
  if (previous.kind !== "workspace_file" || !previous.fileId || previous.runtimeId === binding.runtimeId) return;
  const matches = (ref: DocumentRef) => ref.kind === "workspace_file" && ref.fileId === previous.fileId;
  const update = (ref: DocumentRef): DocumentRef => matches(ref) ? { ...ref, ...binding } : ref;
  const state = useDocModeStore.getState();
  useDocModeStore.setState({
    openBySession: Object.fromEntries(Object.entries(state.openBySession).map(([session, refs]) => [session, refs.map(update)])),
    activeBySession: Object.fromEntries(Object.entries(state.activeBySession).map(([session, ref]) => [session, ref ? update(ref) : null])),
  });
}

export function adoptWorkspaceFileDocumentIdentity(workspaceId: string, previous: DocumentRef, identity: { readonly fileId: string; readonly directoryId?: string }): void {
  const { fileId, directoryId } = identity;
  if (previous.kind !== "workspace_file" || previous.fileId) return;
  const state = useDocModeStore.getState();
  const previousKey = documentDraftKey(previous);
  const matches = (ref: DocumentRef) => documentDraftKey(ref) === previousKey
    && ref.workDir === previous.workDir && ref.runtimeId === previous.runtimeId;
  const sessions = Object.entries(state.openBySession).filter(([, refs]) => refs.some(matches)).map(([session]) => session);
  if (sessions.length === 0) return;
  const draftsByDocument = { ...state.draftsByDocument };
  const identified = adoptDocumentDraftIdentity({ ...previous, fileId, directoryId }, draftsByDocument);
  if (!identified.fileId) return;
  const key = documentDraftKey(identified);
  const openBySession = { ...state.openBySession };
  const activeBySession = { ...state.activeBySession };
  const locationBySession = { ...state.locationBySession };
  const identify = (ref: DocumentRef) => matches(ref) ? { ...ref, fileId, directoryId } : ref;
  for (const session of sessions) {
    const opened = new Map<string, DocumentRef>();
    for (const ref of openBySession[session] ?? []) {
      const next = identify(ref);
      opened.set(documentDraftKey(next), next);
    }
    openBySession[session] = [...opened.values()];
    const active = activeBySession[session];
    if (active && matches(active)) activeBySession[session] = identify(active);
    const location = locationBySession[session];
    if (location?.documentKey === previousKey) locationBySession[session] = { ...location, documentKey: key };
  }
  useChatWorkspaceLayoutStore.setState((layout) => {
    const readingPositions = { ...layout.readingPositions };
    for (const session of sessions) {
      const from = JSON.stringify([workspaceId, session, legacyDocumentDraftKey(previous)]);
      const to = JSON.stringify([workspaceId, session, key]);
      if (readingPositions[to] === undefined && readingPositions[from] !== undefined) readingPositions[to] = readingPositions[from];
    }
    return { readingPositions };
  });
  useDocModeStore.setState({ openBySession, activeBySession, draftsByDocument, locationBySession });
}
