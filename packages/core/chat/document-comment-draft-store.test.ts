// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";
import { defaultStorage } from "../platform/storage";
import { resetAllRegisteredDrafts } from "../drafts/cleanup-registry";
import { documentCommentScopeKey } from "./document-comment-identity";
import { useDocumentCommentDraftStore } from "./document-comment-draft-store";

const document = { kind: "issue", id: "document-a" } as const;
const scope = documentCommentScopeKey("workspace-a", document);
const original = {
  content: "Saved annotation draft",
  anchor: { quote: "Original passage", start: 12, prefix: "Before ", suffix: " after", revision: 3 },
  submissionId: "3f99af2b-921c-4194-8dda-9e41159d8e4c",
};

beforeEach(() => useDocumentCommentDraftStore.setState({ drafts: {} }));

describe("document annotation drafts", () => {
  it("keeps a file annotation draft scope stable across provider rebinding", () => {
    const oldRef = { kind: "workspace_file", id: "old", fileId: "stable-file", runtimeId: "old-runtime", taskId: "old-task", workDir: "/old", relativePath: "notes.md" } as const;
    const newRef = { ...oldRef, id: "new", runtimeId: "new-runtime", taskId: "new-task", workDir: "/new", relativePath: "notes.md" } as const;

    expect(documentCommentScopeKey("workspace-a", oldRef)).toBe(documentCommentScopeKey("workspace-a", newRef));
  });
  it("recovers an original selection and retry identity even if another persisted entry is malformed", async () => {
    await defaultStorage.setItem("multica_document_comment_drafts", JSON.stringify({ state: { drafts: { [scope]: original, broken: { content: 42 } } }, version: 0 }));

    await useDocumentCommentDraftStore.persist.rehydrate();

    expect(useDocumentCommentDraftStore.getState().drafts).toEqual({ [scope]: original });
    expect(useDocumentCommentDraftStore.getState().drafts[documentCommentScopeKey("another-workspace", document)]).toBeUndefined();
  });

  it("keeps a newer note typed while an earlier submission is waiting", () => {
    const { setDraft, clearDraft } = useDocumentCommentDraftStore.getState();
    setDraft(scope, original);
    const sent = useDocumentCommentDraftStore.getState().drafts[scope];
    if (!sent) throw new TypeError("The draft was not stored");
    setDraft(scope, { content: "Typed during the request" });

    clearDraft(scope, sent);

    expect(useDocumentCommentDraftStore.getState().drafts[scope]?.content).toBe("Typed during the request");
  });

  it("removes annotation drafts when the account's registered drafts are cleared", () => {
    useDocumentCommentDraftStore.getState().setDraft(scope, original);

    resetAllRegisteredDrafts();

    expect(useDocumentCommentDraftStore.getState().drafts).toEqual({});
  });
});
