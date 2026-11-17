import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useChatWorkspaceLayoutStore } from "@multica/core/chat";
import { I18nProvider } from "@multica/core/i18n/react";
import enChat from "../../locales/en/chat.json";
import { ChatWorkbench } from "./chat-workbench";

function setup({ width = 1200, documentKey = "document-1" }: {
  width?: number;
  documentKey?: string | null;
} = {}) {
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function () {
    return new DOMRect(0, 0, width, 800);
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
      <ChatWorkbench scope="workspace-1:session-1" documentKey={documentKey}
        conversation={<textarea aria-label="Message draft" defaultValue="Unsent request" />}
        document={<textarea aria-label="Document body" defaultValue="Unpublished draft" />}
        files={<p>Reference files</p>} />
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.restoreAllMocks();
  useChatWorkspaceLayoutStore.setState({ layouts: {} });
});

describe("chat workbench", () => {
  it("keeps file operations visible when the selected file receives a new path", async () => {
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => new DOMRect(0, 0, 1200, 800));
    function Surface({ path }: { readonly path: string }) {
      return <I18nProvider locale="en" resources={{ en: { chat: enChat } }}><ChatWorkbench scope="rename-session" documentKey="file:stable-id" selectedDocument={{ kind: "workspace_file", id: "stable-id", fileId: "stable-id", label: path, relativePath: path }} conversation={null} document={<p>Document</p>} files={<p>File operations</p>} /></I18nProvider>;
    }
    const view = render(<Surface path="original.md" />);
    fireEvent.click(screen.getByRole("button", { name: "Files" }));
    expect(await screen.findByRole("dialog")).toBeVisible();

    view.rerender(<Surface path="moved.md" />);

    expect(screen.getByRole("dialog")).toBeVisible();
    expect(screen.getByText("File operations")).toBeVisible();
  });
  it("gives the conversation the available pane when no document is selected", () => {
    setup({ documentKey: null });

    expect(screen.getByRole("region", { name: "Conversation" })).toBeVisible();
    expect(screen.queryByRole("region", { name: "Document" })).not.toBeInTheDocument();
    expect(screen.queryByRole("separator")).not.toBeInTheDocument();
  });

  it("keeps both drafts mounted when focusing and restoring a wide document", () => {
    setup();
    const message = screen.getByRole("textbox", { name: "Message draft" });
    const document = screen.getByRole("textbox", { name: "Document body" });
    fireEvent.change(message, { target: { value: "Request in progress" } });
    fireEvent.change(document, { target: { value: "Edited draft" } });

    fireEvent.click(screen.getByRole("button", { name: "Focus document" }));
    expect(screen.getByRole("region", { name: "Conversation" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Move chat" })).toBeVisible();
    expect(screen.getByRole("textbox", { name: "Document body" })).toBe(document);
    fireEvent.click(screen.getByRole("button", { name: "Show beside chat" }));

    expect(screen.getByRole("textbox", { name: "Message draft" })).toBe(message);
    expect(message).toHaveValue("Request in progress");
    expect(document).toHaveValue("Edited draft");
  });

  it("switches compact panes without dropping either unsaved draft", () => {
    setup({ width: 600 });
    const document = screen.getByRole("textbox", { name: "Document body" });
    fireEvent.change(document, { target: { value: "Compact document draft" } });

    fireEvent.click(screen.getByRole("button", { name: "Conversation" }));
    const message = screen.getByRole("textbox", { name: "Message draft" });
    fireEvent.change(message, { target: { value: "Compact message draft" } });
    fireEvent.click(screen.getByRole("button", { name: "Document" }));

    expect(screen.getByRole("textbox", { name: "Document body" })).toBe(document);
    expect(document).toHaveValue("Compact document draft");
    expect(message).toHaveValue("Compact message draft");
  });

  it("preserves composer focus for transcript actions only in a focused document", async () => {
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      return this.getAttribute("data-slot") === "resizable-handle"
        ? new DOMRect(600, 0, 0, 800)
        : new DOMRect(0, 0, 1200, 800);
    });
    const pointerFocusBlocked = vi.fn();
    render(<I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
      <ChatWorkbench scope="focused-actions" documentKey="document"
        conversation={<><div data-chat-transcript><button onPointerDown={(event) => pointerFocusBlocked(event.defaultPrevented)}>Copy reply</button></div><div data-chat-composer-editor><textarea aria-label="Input" /></div></>}
        document={<button>Document action</button>} files={null} />
    </I18nProvider>);
    fireEvent.click(screen.getByRole("button", { name: "Focus document" }));
    screen.getByRole("textbox", { name: "Input" }).focus();
    expect(fireEvent.pointerDown(screen.getByRole("button", { name: "Copy reply" }))).toBe(false);
    expect(pointerFocusBlocked).toHaveBeenLastCalledWith(true);
    fireEvent.pointerUp(screen.getByRole("button", { name: "Copy reply" }));
    expect(fireEvent.pointerDown(screen.getByRole("button", { name: "Document action" }))).toBe(true);
    fireEvent.pointerUp(screen.getByRole("button", { name: "Document action" }));
    fireEvent.click(screen.getByRole("button", { name: "Show beside chat" }));
    expect(useChatWorkspaceLayoutStore.getState().layouts["focused-actions"]?.mode).toBe("parallel");
    await waitFor(() => expect(screen.queryByRole("button", { name: "Show beside chat" })).not.toBeInTheDocument());
    fireEvent.pointerDown(screen.getByRole("button", { name: "Copy reply" }));
    expect(pointerFocusBlocked).toHaveBeenLastCalledWith(false);
  });

  it("opens the file browser only on request and closes it from its own control", async () => {
    setup();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    const trigger = screen.getByRole("button", { name: "Files" });
    fireEvent.click(trigger);
    expect(await screen.findByRole("dialog", { name: "Files" })).toBeVisible();
    expect(screen.getByText("Reference files")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Close" }));

    expect(trigger).toHaveAttribute("aria-expanded", "false");
  });
});
