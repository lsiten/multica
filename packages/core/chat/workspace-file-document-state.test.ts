// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";
import { documentDraftKey, selectDocumentDraft, useDocModeStore, type DocumentRef } from "./doc-mode-store";
import { restoreDocModeState } from "./doc-mode-persistence";
import { adoptWorkspaceFileDocumentIdentity, applyWorkspaceFileOperationResult, rebindWorkspaceFileDocument, updateWorkspaceFileDocumentLocation } from "./workspace-file-document-state";
import { useChatWorkspaceLayoutStore } from "./workspace-layout-store";

const original: DocumentRef = {
  kind: "workspace_file", id: "run-a:notes.md", taskId: "run-a", workDir: "/workspace",
  relativePath: "notes.md", fileId: "b76700bd-cbf0-430b-be84-d5a90aa6d275",
};
const relocated: DocumentRef = { ...original, id: "run-b:docs/notes.md", taskId: "run-b", relativePath: "docs/notes.md" };
const draft = { kind: "workspace_file", content: "Unpublished revision", baseContent: "Original" } as const;

beforeEach(() => {
  useDocModeStore.setState({ activeBySession: {}, openBySession: {}, draftsByDocument: {}, locationBySession: {} });
  useChatWorkspaceLayoutStore.setState({ readingPositions: {} });
});

describe("stable workspace file document state", () => {
  it("rebinds every open tab to a replacement runtime without changing file identity or draft key", () => {
    const { openDoc, setDocumentDraft } = useDocModeStore.getState();
    openDoc("chat", { ...original, runtimeId: "old-runtime" });
    setDocumentDraft({ ...original, runtimeId: "old-runtime" }, draft);

    rebindWorkspaceFileDocument({ ...original, runtimeId: "old-runtime" }, { runtimeId: "new-runtime", taskId: "new-task", workDir: "/new-root" });

    const rebound = useDocModeStore.getState().activeBySession.chat;
    expect(rebound).toMatchObject({ fileId: original.fileId, runtimeId: "new-runtime", taskId: "new-task", workDir: "/new-root" });
    expect(documentDraftKey(rebound ?? original)).toBe(documentDraftKey(original));
    expect(selectDocumentDraft(rebound ?? original)(useDocModeStore.getState())).toEqual(draft);
  });
  it("updates a moved file in every open conversation without accepting an older location or touching a replacement", () => {
    const { openDoc, setDocumentDraft } = useDocModeStore.getState();
    const current = { ...original, directoryRevision: 4 };
    openDoc("chat", current, { offset: 42, line: 3, text: "Passage" });
    openDoc("other-chat", current);
    setDocumentDraft(current, draft);
    const replacement = { ...original, fileId: "ad37fb46-4659-48ba-8188-639183658c6f" };
    openDoc("replacement-chat", replacement);
    if (!current.fileId) throw new TypeError("Fixture requires a file identity");

    updateWorkspaceFileDocumentLocation(current, { fileId: current.fileId, directoryRevision: 5, relativePath: "docs/notes.md" });
    updateWorkspaceFileDocumentLocation(current, { fileId: current.fileId, directoryRevision: 4, relativePath: "obsolete.md" });

    for (const session of ["chat", "other-chat"]) {
      expect(useDocModeStore.getState().activeBySession[session]).toMatchObject({ id: current.fileId, relativePath: "docs/notes.md", directoryRevision: 5 });
      expect(useDocModeStore.getState().openBySession[session]?.[0]?.relativePath).toBe("docs/notes.md");
    }
    expect(useDocModeStore.getState().activeBySession["replacement-chat"]?.relativePath).toBe("notes.md");
    expect(selectDocumentDraft(current)(useDocModeStore.getState())).toEqual(draft);
    expect(useDocModeStore.getState().locationBySession.chat?.offset).toBe(42);
  });
  it("applies move receipts by stable file identity and closes trashed tabs without dropping drafts", () => {
    const { openDoc, setDocumentDraft } = useDocModeStore.getState();
    openDoc("chat", original);
    setDocumentDraft(original, draft);
    applyWorkspaceFileOperationResult({
      kind: "file_operation", command_id: "move-1", operation_id: "op-1", operation: "move",
      path: "notes.md", destination: "docs/notes.md", file_id: original.fileId, directory_id: "a".repeat(64), directory_revision: 7, directory: false,
    });
    expect(useDocModeStore.getState().activeBySession.chat).toMatchObject({ relativePath: "docs/notes.md", directoryRevision: 7 });
    expect(selectDocumentDraft(original)(useDocModeStore.getState())).toEqual(draft);

    applyWorkspaceFileOperationResult({
      kind: "file_operation", command_id: "trash-1", operation: "trash",
      path: "docs/notes.md", file_id: original.fileId, directory_revision: 8, directory: false,
    });
    expect(useDocModeStore.getState().activeBySession.chat).toBeNull();
    expect(useDocModeStore.getState().openBySession.chat).toEqual([]);
    expect(selectDocumentDraft(original)(useDocModeStore.getState())).toEqual(draft);
  });
  it("gives a replacement at the same path its own editor mount without borrowing the old draft", () => {
    const { openDoc, setDocumentDraft } = useDocModeStore.getState();
    openDoc("chat", original);
    const previousMount = useDocModeStore.getState().activeBySession.chat?.id;
    setDocumentDraft(original, draft);
    const replacement = { ...original, fileId: "ad37fb46-4659-48ba-8188-639183658c6f" };

    openDoc("chat", replacement);

    expect(useDocModeStore.getState().activeBySession.chat?.id).not.toBe(previousMount);
    expect(useDocModeStore.getState().openBySession.chat).toHaveLength(2);
    expect(selectDocumentDraft(replacement)(useDocModeStore.getState())).toBeUndefined();
    expect(selectDocumentDraft(original)(useDocModeStore.getState())).toEqual(draft);
  });
  it("reopens the same file after relocation without duplicating its tab or losing its draft", () => {
    const { openDoc, setDocumentDraft } = useDocModeStore.getState();
    openDoc("chat", original);
    setDocumentDraft(original, draft);

    openDoc("chat", relocated);

    expect(useDocModeStore.getState().openBySession.chat).toEqual([{ ...relocated, id: original.fileId }]);
    expect(selectDocumentDraft(relocated)(useDocModeStore.getState())).toEqual(draft);
  });

  it("recovers a persisted path-keyed draft when its tab already has a server file identity", () => {
    const restored = restoreDocModeState({
      activeBySession: { chat: original }, openBySession: { chat: [original] },
      draftsByDocument: { "workspace_file:run-a:notes.md:run-a::notes.md": draft },
    });

    expect(restored.draftsByDocument[documentDraftKey(relocated)]).toEqual(draft);
  });

  it("adopts a legacy open tab's identity while preserving its draft, source location and reading position", () => {
    const { fileId, ...legacy } = original;
    if (!fileId) throw new TypeError("Fixture requires a file identity");
    const { openDoc, setDocumentDraft } = useDocModeStore.getState();
    openDoc("chat", legacy, { offset: 42, line: 3, text: "Passage" });
    setDocumentDraft(legacy, draft);
    useChatWorkspaceLayoutStore.getState().setReadingPosition(JSON.stringify(["workspace", "chat", documentDraftKey(legacy)]), 880);

    adoptWorkspaceFileDocumentIdentity("workspace", legacy, { fileId });

    expect(useDocModeStore.getState().activeBySession.chat).toEqual(original);
    expect(selectDocumentDraft(original)(useDocModeStore.getState())).toEqual(draft);
    expect(useDocModeStore.getState().locationBySession.chat).toEqual({ offset: 42, line: 3, text: "Passage", documentKey: documentDraftKey(original) });
    expect(useChatWorkspaceLayoutStore.getState().readingPositions[JSON.stringify(["workspace", "chat", documentDraftKey(original)])]).toBe(880);
  });

  it("retains both independently edited drafts when a legacy tab resolves to an already edited file", () => {
    const { fileId, ...legacy } = original;
    if (!fileId) throw new TypeError("Fixture requires a file identity");
    const { openDoc, setDocumentDraft } = useDocModeStore.getState();
    openDoc("chat", legacy);
    setDocumentDraft(legacy, draft);
    openDoc("chat", relocated);
    const otherDraft = { ...draft, content: "Independent run's edits" };
    setDocumentDraft(relocated, otherDraft);

    adoptWorkspaceFileDocumentIdentity("workspace", legacy, { fileId });

    expect(useDocModeStore.getState().openBySession.chat).toEqual([legacy, { ...relocated, id: relocated.fileId }]);
    expect(selectDocumentDraft(legacy)(useDocModeStore.getState())).toEqual(draft);
    expect(selectDocumentDraft(relocated)(useDocModeStore.getState())).toEqual(otherDraft);
  });

  it("ignores a late identity response after the original tab was closed", () => {
    const { fileId, ...legacy } = original;
    if (!fileId) throw new TypeError("Fixture requires a file identity");
    const { openDoc, closeDoc } = useDocModeStore.getState();
    openDoc("chat", legacy);
    closeDoc("chat");
    openDoc("chat", { kind: "issue", id: "another-document" });

    adoptWorkspaceFileDocumentIdentity("workspace", legacy, { fileId });

    expect(useDocModeStore.getState().openBySession.chat).toEqual([{ kind: "issue", id: "another-document" }]);
    expect(useDocModeStore.getState().activeBySession.chat?.id).toBe("another-document");
  });
});
