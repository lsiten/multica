// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";
import { documentDraftKey, useDocModeStore, type DocumentRef } from "./doc-mode-store";
import { defaultStorage } from "../platform/storage";

const first: DocumentRef = { kind: "workspace_file", id: "file-1", taskId: "task-1", relativePath: "notes.md", label: "notes.md" };
const second: DocumentRef = { kind: "workspace_file", id: "file-2", taskId: "task-2", relativePath: "notes.md", label: "notes.md" };

beforeEach(() => {
  useDocModeStore.setState({ activeBySession: {}, openBySession: {}, draftsByDocument: {}, workingBySession: {}, diffViewBySession: {} });
});

describe("conversation document tabs", () => {
  it("retains same-name resources from different directories and isolates conversations", () => {
    const { openDoc } = useDocModeStore.getState();
    openDoc("session-1", first);
    openDoc("session-1", second);
    openDoc("session-2", { kind: "issue", id: "issue-1" });

    expect(useDocModeStore.getState().openBySession["session-1"]).toEqual([first, second]);
    expect(useDocModeStore.getState().activeBySession["session-2"]?.id).toBe("issue-1");
  });

  it("returns to chat without closing resources and preserves order when a tab is selected again", () => {
    const { openDoc, selectChat } = useDocModeStore.getState();
    openDoc("session-1", first);
    openDoc("session-1", second);
    selectChat("session-1");
    expect(useDocModeStore.getState().activeBySession["session-1"]).toBeNull();

    openDoc("session-1", first);

    expect(useDocModeStore.getState().openBySession["session-1"]).toEqual([first, second]);
    expect(useDocModeStore.getState().activeBySession["session-1"]).toEqual(first);
  });

  it("closes a resource without discarding its unpublished draft", () => {
    const { openDoc, closeTab, setDocumentDraft } = useDocModeStore.getState();
    openDoc("session-1", first);
    openDoc("session-1", second);
    const draft = { kind: "workspace_file", content: "Unpublished changes", baseContent: "Original" } as const;
    setDocumentDraft(second, draft);

    closeTab("session-1", documentDraftKey(second));

    expect(useDocModeStore.getState().activeBySession["session-1"]).toEqual(first);
    expect(useDocModeStore.getState().draftsByDocument[documentDraftKey(second)]).toEqual(draft);
  });

  it("reorders tabs without changing their selected resource", () => {
    const { openDoc, moveTab } = useDocModeStore.getState();
    openDoc("session-1", first);
    openDoc("session-1", second);

    moveTab("session-1", documentDraftKey(second), documentDraftKey(first));

    expect(useDocModeStore.getState().openBySession["session-1"]).toEqual([second, first]);
    expect(useDocModeStore.getState().activeBySession["session-1"]).toEqual(second);
  });

  it("restores resource tabs and the selected chat after rehydration", async () => {
    const { openDoc, selectChat } = useDocModeStore.getState();
    openDoc("session-1", first);
    selectChat("session-1");
    const saved = await defaultStorage.getItem("multica_doc_mode");
    if (saved === null) throw new Error("Document tabs were not persisted");
    useDocModeStore.setState({ activeBySession: {}, openBySession: {} });
    await defaultStorage.setItem("multica_doc_mode", saved);

    await useDocModeStore.persist.rehydrate();

    expect(useDocModeStore.getState().openBySession["session-1"]).toEqual([first]);
    expect(useDocModeStore.getState().activeBySession["session-1"]).toBeNull();
  });
});
