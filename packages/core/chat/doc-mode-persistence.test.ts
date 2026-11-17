// @vitest-environment node
import { describe, expect, it } from "vitest";
import { restoreDocModeState } from "./doc-mode-persistence";

describe("document workspace persistence", () => {
  it("promotes a legacy active document to an actual resource tab while retaining its draft", () => {
    const document = { kind: "issue", id: "document-a", label: "Proposal" };
    const draft = { kind: "version", content: "Unsent changes", baseContent: "Original", baseRevision: 1, contentType: "text/markdown" };
    const restored = restoreDocModeState({ activeBySession: { chat: document }, draftsByDocument: { "issue:document-a:::": draft } });
    expect(restored.openBySession.chat).toEqual([document]);
    expect(restored.activeBySession.chat).toEqual(document);
    expect(restored.draftsByDocument["issue:document-a:::"]).toEqual(draft);
  });

  it("retains valid sibling tabs and drafts while discarding unsupported persisted records", () => {
    const document = { kind: "attachment", id: "attachment-a" };
    const restored = restoreDocModeState({
      activeBySession: { bad: { kind: "unknown", id: "bad" }, chat: null },
      openBySession: { chat: [document, { kind: "issue" }, document, null], broken: "bad" },
      draftsByDocument: { valid: { kind: "workspace_file", content: "draft", baseContent: "base" }, bad: { kind: "version", baseRevision: -1 } },
    });
    expect(restored.openBySession).toEqual({ chat: [document] });
    expect(restored.activeBySession).toEqual({ bad: null, chat: null });
    expect(Object.keys(restored.draftsByDocument)).toEqual(["valid"]);
  });
});
