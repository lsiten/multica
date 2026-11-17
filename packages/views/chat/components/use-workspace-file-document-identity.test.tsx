// @vitest-environment jsdom
import { renderHook } from "@testing-library/react";
import { beforeEach, expect, it } from "vitest";
import { documentDraftKey, useDocModeStore, type DocumentRef } from "@multica/core/chat";
import { useWorkspaceFileDocumentIdentity } from "./use-workspace-file-document-identity";

const legacy: DocumentRef = { kind: "workspace_file", id: "task:notes.md", taskId: "task", relativePath: "notes.md", workDir: "/work", runtimeId: "runtime" };
const fileId = "b76700bd-cbf0-430b-be84-d5a90aa6d275";
const content = { text: "Current body", encoding: "utf8", offset: 0, next_offset: 12, size: 12, has_more: false } as const;
beforeEach(() => useDocModeStore.setState({ openBySession: {}, activeBySession: {}, draftsByDocument: {}, locationBySession: {} }));

it("keeps a legacy draft separate when the old path may now contain a replacement", () => {
  useDocModeStore.getState().openDoc("chat", legacy);
  useDocModeStore.getState().setDocumentDraft(legacy, { kind: "workspace_file", content: "Unpublished old edits", baseContent: "Old body" });

  renderHook(() => useWorkspaceFileDocumentIdentity({ workspaceId: "workspace", document: legacy, editing: false, page: { file_id: fileId, file_identity_ambiguous: true, path: "notes.md", content } }));

  expect(useDocModeStore.getState().activeBySession.chat?.fileId).toBeUndefined();
  expect(useDocModeStore.getState().draftsByDocument[documentDraftKey(legacy)]?.content).toBe("Unpublished old edits");
});

it("follows a known file's moved path while editing without reopening or replacing its draft", () => {
  const known = { ...legacy, fileId, directoryRevision: 2 };
  useDocModeStore.getState().openDoc("chat", known);
  useDocModeStore.getState().setDocumentDraft(known, { kind: "workspace_file", content: "Unpublished edits", baseContent: "Old body" });

  renderHook(() => useWorkspaceFileDocumentIdentity({ workspaceId: "workspace", document: known, editing: true, page: { file_id: fileId, directory_revision: 3, path: "docs/notes.md", content } }));

  expect(useDocModeStore.getState().activeBySession.chat).toMatchObject({ fileId, relativePath: "docs/notes.md", directoryRevision: 3 });
  expect(useDocModeStore.getState().draftsByDocument[documentDraftKey(known)]?.content).toBe("Unpublished edits");
});
