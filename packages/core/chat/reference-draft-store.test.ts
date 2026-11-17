// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";
import { defaultStorage } from "../platform/storage";
import { composeChatReferences, type ChatReference } from "./document-reference";
import { useChatReferenceDraftStore } from "./reference-draft-store";

const scope = JSON.stringify(["ws-a", "session-a"]);
const reference: ChatReference = {
  id: "reference-a", label: "Plan", content: "Original selected passage\n```embedded```",
  source: { workspaceId: "ws-a", resource: { kind: "issue", id: "issue-a" }, revision: 7, state: "saved", extent: "selection" },
};
const messageReference: ChatReference = {
  id: "quoted-message", label: "Previous reply", content: "Original reply snapshot",
  messageSource: { workspaceId: "ws-a", sessionId: "source-chat", messageId: "source-message", role: "assistant", createdAt: "2026-11-15T13:00:00Z", attachmentIds: ["source-file"] },
};

beforeEach(() => useChatReferenceDraftStore.setState({ byScope: {} }));

describe("reference drafts", () => {
  it.each([reference, messageReference])("restores exact source and snapshot across reloads without mixing conversations: $id", async (reference) => {
    useChatReferenceDraftStore.getState().add(scope, reference);
    useChatReferenceDraftStore.getState().add("another-scope", { ...reference, id: "reference-b", content: "Other conversation" });
    const persisted = await defaultStorage.getItem("multica_chat_reference_drafts");
    useChatReferenceDraftStore.setState({ byScope: {} });
    await defaultStorage.setItem("multica_chat_reference_drafts", persisted ?? "");
    await useChatReferenceDraftStore.persist.rehydrate();

    expect(useChatReferenceDraftStore.getState().byScope[scope]).toEqual([reference]);
    expect(useChatReferenceDraftStore.getState().byScope["another-scope"]?.[0]?.content).toBe("Other conversation");
  });

  it("consumes only sent references while retaining references added during the request", () => {
    const { add, remove } = useChatReferenceDraftStore.getState();
    add(scope, reference);
    add(scope, { ...reference, id: "later-reference", content: "New selection" });

    remove(scope, ["reference-a"]);

    expect(useChatReferenceDraftStore.getState().byScope[scope]?.map((item) => item.id)).toEqual(["later-reference"]);
  });

  it("sends provenance and the original snapshot without letting embedded fences end the quote or moving /note", () => {
    const composed = composeChatReferences("/note Check this passage", [reference]);

    expect(composed.startsWith("/note\n")).toBe(true);
    expect(composed).toContain('"revision":7');
    expect(composed).toContain('"id":"issue-a"');
    expect(composed).toContain("````text\nOriginal selected passage\n```embedded```\n````");
    expect(composed.endsWith("Check this passage")).toBe(true);
  });
});
