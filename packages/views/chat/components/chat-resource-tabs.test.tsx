import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useDocModeStore, useChatWorkspaceLayoutStore, DOCUMENT_TAB_MIME, documentTabDragValue, documentDraftKey, type DocumentRef } from "@multica/core/chat";
import { I18nProvider } from "@multica/core/i18n/react";
import enChat from "../../locales/en/chat.json";
import { ChatResourceTabs } from "./chat-resource-tabs";
import { ChatWorkbench } from "./chat-workbench";

const document: DocumentRef = { kind: "issue", id: "issue-1", label: "outline.md" };
const scope = "workspace:session-1";

function Surface() {
  const active = useDocModeStore((state) => state.activeBySession["session-1"] ?? null);
  return <ChatWorkbench scope={scope} documentKey={active ? documentDraftKey(active) : null} selectedDocument={active}
    tabs={<ChatResourceTabs sessionId="session-1" scope={scope} />}
    conversation={<textarea aria-label="Message draft" defaultValue="Keep this request" />}
    document={active ? <p>Document contents</p> : null} files={<p>Files</p>} />;
}

function setup() {
  return render(<I18nProvider locale="en" resources={{ en: { chat: enChat } }}><Surface /></I18nProvider>);
}

beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => new DOMRect(0, 0, 1200, 900));
  useChatWorkspaceLayoutStore.setState({ layouts: {} });
  useDocModeStore.setState({ activeBySession: {}, openBySession: {}, draftsByDocument: {} });
  useDocModeStore.getState().openDoc("session-1", document);
});

describe("chat resource tabs", () => {
  it("switches between the full conversation and a document with the same message draft", () => {
    setup();
    const composer = screen.getByRole("textbox", { name: "Message draft" });
    fireEvent.change(composer, { target: { value: "Edited before switching tabs" } });

    fireEvent.click(screen.getByRole("tab", { name: "Conversation" }));
    expect(screen.queryByText("Document contents")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "outline.md" }));

    expect(screen.getByText("Document contents")).toBeVisible();
    expect(screen.getByRole("button", { name: "Move chat" })).toBeVisible();
    expect(screen.getByRole("textbox", { name: "Message draft" })).toBe(composer);
    expect(composer).toHaveValue("Edited before switching tabs");
  });

  it.each(["session-1", "another-session"])("accepts a document drop only from its own conversation: %s", (sourceSession) => {
    setup();

    fireEvent.drop(screen.getByRole("tablist"), { dataTransfer: {
      types: [DOCUMENT_TAB_MIME], getData: () => documentTabDragValue(sourceSession, document),
    } });

    expect(screen.queryByRole("button", { name: "Move chat" }) !== null).toBe(sourceSession === "session-1");
    expect(screen.getByRole("textbox", { name: "Message draft" })).toHaveValue("Keep this request");
  });

  it("keeps the current draft when the floating conversation is collapsed and expanded", () => {
    setup();
    fireEvent.click(screen.getByRole("tab", { name: "outline.md" }));
    const composer = screen.getByRole("textbox", { name: "Message draft" });

    fireEvent.click(screen.getByRole("button", { name: "Collapse chat" }));
    expect(screen.queryByRole("textbox", { name: "Message draft" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Expand chat" }));

    expect(screen.getByRole("textbox", { name: "Message draft" })).toBe(composer);
    expect(composer).toHaveValue("Keep this request");
  });
});
