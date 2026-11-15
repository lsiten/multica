// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";
import {
  documentDraftKey,
  selectActiveDocument,
  selectDiffView,
  selectDocumentDraft,
  selectWorking,
  useDocModeStore,
} from "./doc-mode-store";

beforeEach(() => {
  useDocModeStore.setState({
    activeBySession: {},
    workingBySession: {},
    diffViewBySession: {},
    draftsByDocument: {},
  });
});

describe("useDocModeStore.openDoc", () => {
  it("stores the active document per session (namespaced)", () => {
    const { openDoc } = useDocModeStore.getState();
    openDoc("session-a", { kind: "issue", id: "issue-1", label: "MUL-1" });
    openDoc("session-b", { kind: "attachment", id: "attach-1", contentType: "image/png" });

    expect(selectActiveDocument("session-a")(useDocModeStore.getState())).toMatchObject({
      kind: "issue",
      id: "issue-1",
    });
    expect(selectActiveDocument("session-b")(useDocModeStore.getState())).toMatchObject({
      kind: "attachment",
      id: "attach-1",
      contentType: "image/png",
    });
    // Other sessions stay untouched.
    expect(selectActiveDocument("session-c")(useDocModeStore.getState())).toBeNull();
  });

  it("replaces the active document in the same session", () => {
    const { openDoc } = useDocModeStore.getState();
    openDoc("session-a", { kind: "issue", id: "issue-1" });
    openDoc("session-a", { kind: "attachment", id: "attach-9" });
    expect(selectActiveDocument("session-a")(useDocModeStore.getState())).toMatchObject({
      kind: "attachment",
      id: "attach-9",
    });
  });
});

describe("useDocModeStore.closeDoc", () => {
  it("clears the active document and the working flag", () => {
    const { openDoc, setWorking, closeDoc } = useDocModeStore.getState();
    openDoc("session-a", { kind: "issue", id: "issue-1" });
    setWorking("session-a", true);
    closeDoc("session-a");

    expect(selectActiveDocument("session-a")(useDocModeStore.getState())).toBeNull();
    expect(selectWorking("session-a")(useDocModeStore.getState())).toBe(false);
  });

  it("keeps unsaved document drafts when the active document closes", () => {
    const doc = { kind: "issue" as const, id: "issue-1" };
    const { openDoc, closeDoc, setDocumentDraft } = useDocModeStore.getState();
    openDoc("session-a", doc);
    setDocumentDraft(doc, {
      kind: "version",
      content: "# Draft",
      baseContent: "# Original",
      baseRevision: 7,
      contentType: "text/markdown",
    });

    closeDoc("session-a");

    expect(selectActiveDocument("session-a")(useDocModeStore.getState())).toBeNull();
    expect(selectDocumentDraft(doc)(useDocModeStore.getState())).toMatchObject({
      content: "# Draft",
      baseRevision: 7,
    });
  });
});

describe("document drafts", () => {
  it("isolates drafts by full document identity and clears only the requested draft", () => {
    const first = { kind: "workspace_file" as const, id: "task-1:src/app.ts", taskId: "task-1", relativePath: "src/app.ts" };
    const second = { ...first, taskId: "task-2" };
    const { setDocumentDraft, clearDocumentDraft } = useDocModeStore.getState();
    setDocumentDraft(first, { kind: "workspace_file", content: "first draft", baseContent: "first" });
    setDocumentDraft(second, { kind: "workspace_file", content: "second draft", baseContent: "second" });

    expect(documentDraftKey(first)).not.toBe(documentDraftKey(second));
    clearDocumentDraft(first);

    expect(selectDocumentDraft(first)(useDocModeStore.getState())).toBeUndefined();
    expect(selectDocumentDraft(second)(useDocModeStore.getState())).toMatchObject({ content: "second draft" });
  });

  it("rehydrates a draft after the store is recreated from persisted state", async () => {
    const doc = { kind: "issue" as const, id: "issue-reload" };
    const { openDoc, closeDoc, setDocumentDraft } = useDocModeStore.getState();
    openDoc("session-a", doc);
    setDocumentDraft(doc, {
      kind: "version",
      content: "# Recovered",
      baseContent: "# Original",
      baseRevision: 3,
      contentType: "text/markdown",
    });
    closeDoc("session-a");
    const persisted = localStorage.getItem("multica_doc_mode");
    expect(persisted).toContain("Recovered");

    useDocModeStore.setState({
      activeBySession: {},
      draftsByDocument: {},
    });
    localStorage.setItem("multica_doc_mode", persisted!);
    await useDocModeStore.persist.rehydrate();

    expect(selectDocumentDraft(doc)(useDocModeStore.getState())).toMatchObject({
      content: "# Recovered",
      baseRevision: 3,
    });
  });
});

describe("useDocModeStore.setWorking (D9: no auto-switch)", () => {
  it("an edit completing (working -> false) does NOT close doc mode", () => {
    const { openDoc, setWorking } = useDocModeStore.getState();
    openDoc("session-a", { kind: "issue", id: "issue-1" });
    setWorking("session-a", true);
    // The agent finishes editing; doc mode must persist (no auto-switch to chat).
    setWorking("session-a", false);

    expect(selectWorking("session-a")(useDocModeStore.getState())).toBe(false);
    expect(selectActiveDocument("session-a")(useDocModeStore.getState())).toMatchObject({
      id: "issue-1",
    });
  });
});

describe("useDocModeStore.setDiffView (D6: off by default)", () => {
  it("diff view defaults to off and toggles only on explicit set", () => {
    expect(selectDiffView("session-a")(useDocModeStore.getState())).toBe(false);
    const { setDiffView } = useDocModeStore.getState();
    setDiffView("session-a", true);
    expect(selectDiffView("session-a")(useDocModeStore.getState())).toBe(true);
    setDiffView("session-a", false);
    expect(selectDiffView("session-a")(useDocModeStore.getState())).toBe(false);
  });
});

describe("selectors", () => {
  it("null sessionId yields stable null/false, not a new object each call", () => {
    const a = selectActiveDocument(null)(useDocModeStore.getState());
    const b = selectActiveDocument(null)(useDocModeStore.getState());
    expect(a).toBe(b);
    expect(a).toBeNull();
    expect(selectWorking(null)(useDocModeStore.getState())).toBe(false);
    expect(selectDiffView(null)(useDocModeStore.getState())).toBe(false);
  });
});
