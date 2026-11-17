// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { useChatWorkspaceLayoutStore, DOCUMENT_TAB_MIME, type DocumentRef } from "@multica/core/chat";
import enChat from "../../locales/en/chat.json";
import { DocumentDockTitle } from "./document-dock-title";

const document: DocumentRef = { kind: "workspace_file", id: "file-1", fileId: "file-1", label: "src/app.ts", relativePath: "src/app.ts" };

beforeEach(() => useChatWorkspaceLayoutStore.setState({ layouts: {} }));

function renderTitle() {
  return render(<I18nProvider locale="en" resources={{ en: { chat: enChat } }}><DocumentDockTitle document={document} wsId="ws-1" sessionId="session-1" /></I18nProvider>);
}

describe("DocumentDockTitle", () => {
  it("provides a keyboard-accessible focused-tab action", () => {
    renderTitle();
    const title = screen.getByRole("button", { name: "Open src/app.ts in a full tab" });
    title.focus();
    fireEvent.click(title);
    expect(useChatWorkspaceLayoutStore.getState().layouts['["ws-1","session-1"]']?.mode).toBe("focused");
  });

  it("keeps the drag payload scoped to the source session and document", () => {
    renderTitle();
    const data = new Map<string, string>();
    fireEvent.dragStart(screen.getByRole("button", { name: "Open src/app.ts in a full tab" }), { dataTransfer: { effectAllowed: "", setData: (type: string, value: string) => data.set(type, value) } });
    expect(data.get(DOCUMENT_TAB_MIME)).toBe("session-1\nworkspace_file:file-1");
    expect(data.get("text/plain")).toBe("src/app.ts");
  });
});
