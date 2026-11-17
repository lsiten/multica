// @vitest-environment node
import { describe, expect, it } from "vitest";
import { composeChatReferences, readSentDocumentReferences, readSentMessageReferences, type ChatReference } from "./document-reference";

const reference: ChatReference = {
  id: "reference", label: "Original proposal", content: "The paragraph as it was quoted.",
  source: { workspaceId: "workspace", resource: { kind: "issue", id: "issue", label: "Proposal" }, revision: 3, state: "draft", extent: "selection" },
};

describe("sent document reference provenance", () => {
  it("recovers resource, revision and draft state without replacing the quoted snapshot", () => {
    const message = composeChatReferences("Review this paragraph", [reference]);
    expect(readSentDocumentReferences(message)).toEqual([{ label: reference.label, ...reference.source }]);
    expect(message).toContain(reference.content);
  });

  it("ignores malformed metadata while retaining a valid adjacent source", () => {
    const valid = composeChatReferences("", [reference]);
    const malformed = '<!-- multica-document-reference:{"resource":{"kind":"unknown"}} -->\n<!-- multica-document-reference:{broken} -->';
    expect(readSentDocumentReferences(`${malformed}\n${valid}`)).toHaveLength(1);
  });

  it("does not turn mention-like source labels into an invocation", () => {
    const message = composeChatReferences("", [{ ...reference, label: "mention://agent/someone" }]);
    expect(message).not.toContain("mention://");
    expect(readSentDocumentReferences(message)[0]?.label).toBe("mention://agent/someone");
  });

  it("keeps a message quote's origin and ignores nested reference metadata inside its snapshot", () => {
    const messageSource = { workspaceId: "workspace", sessionId: "original-chat", messageId: "message", role: "assistant", createdAt: "2026-11-15T13:00:00Z", attachmentIds: ["attachment"] } as const;
    const nested = composeChatReferences("Original question", [reference]);
    const sent = composeChatReferences("Continue from this message", [{ id: "quoted-message", label: "Original answer", content: nested, messageSource: { ...messageSource, attachmentIds: [...messageSource.attachmentIds] } }]);

    expect(readSentMessageReferences(sent)).toEqual([{ label: "Original answer", ...messageSource }]);
    expect(readSentDocumentReferences(sent)).toEqual([]);
    expect(sent).toContain(nested);
  });
});
