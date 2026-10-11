// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChat from "../../locales/en/chat.json";
import { useDocModeStore } from "@multica/core/chat";
import { DocModeFileList } from "./doc-file-list";

const TEST_RESOURCES = { en: { common: enCommon, chat: enChat } };

// The file list is fed by the same context items the composer tracks. A
// hoisted mutable mock lets each test steer the returned set (including the
// empty case) without a live navigation + query graph.
const contextItems = vi.hoisted(() => ({
  current: [] as Array<{
    id: string;
    label: string;
    type: "issue" | "project" | "attachment";
    description?: string;
  }>,
}));
vi.mock("./use-chat-context-items", () => ({
  useChatContextItems: () => contextItems.current,
}));

function renderList() {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <DocModeFileList wsId="ws-1" />
    </I18nProvider>,
  );
}

beforeEach(() => {
  useDocModeStore.setState({
    activeByWorkspace: {},
    workingByWorkspace: {},
    diffViewByWorkspace: {},
  });
  contextItems.current = [
    { id: "issue-1", label: "MUL-1", type: "issue", description: "Fix login" },
    { id: "issue-2", label: "MUL-2", type: "issue", description: "Add i18n" },
    { id: "att-1", label: "report.pdf", type: "attachment" },
    { id: "proj-1", label: "Acme", type: "project" },
  ];
});

describe("DocModeFileList (doc-mode entry B)", () => {
  it("lists issue + attachment documents and drops projects", () => {
    renderList();
    // Issue rows prefer the human title; attachment rows show the name.
    expect(screen.getByText("Fix login")).toBeInTheDocument();
    expect(screen.getByText("Add i18n")).toBeInTheDocument();
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    // Projects are not openable in doc mode.
    expect(screen.queryByText("Acme")).not.toBeInTheDocument();
  });

  it("opens the clicked document in doc mode (openDoc)", () => {
    renderList();
    fireEvent.click(screen.getByText("report.pdf"));
    const active = useDocModeStore.getState().activeByWorkspace["ws-1"];
    expect(active).toMatchObject({ kind: "attachment", id: "att-1" });
  });

  it("filters documents by the filter box (client-side)", () => {
    renderList();
    // All four openable docs are present initially.
    expect(screen.getByText("Fix login")).toBeInTheDocument();
    // Filtering to "login" narrows to just that issue.
    fireEvent.change(screen.getByLabelText(enChat.doc_file_list.filter_placeholder), {
      target: { value: "login" },
    });
    expect(screen.getByText("Fix login")).toBeInTheDocument();
    expect(screen.queryByText("Add i18n")).not.toBeInTheDocument();
    expect(screen.queryByText("report.pdf")).not.toBeInTheDocument();
  });

  it("shows a no-match state when the filter excludes everything", () => {
    renderList();
    fireEvent.change(screen.getByLabelText(enChat.doc_file_list.filter_placeholder), {
      target: { value: "zzz-nothing" },
    });
    expect(screen.getByText(enChat.doc_file_list.no_match)).toBeInTheDocument();
  });
});
