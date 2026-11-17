// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor, act, within } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import enCommon from "../../locales/en/common.json";
import enChat from "../../locales/en/chat.json";
import { useDocModeStore, useWorkspaceFileEditStore } from "@multica/core/chat";
import type { ChatSessionFilesResponse } from "@multica/core/types";
import { DocModeSurface } from "./doc-mode-surface";
import { applyReviewFileEdit, requestReviewPage } from "../../platform/local-review-pages";
import { ApiError, setApiInstance } from "@multica/core/api";

const TEST_RESOURCES = { en: { common: enCommon, chat: enChat } };
beforeEach(() => useWorkspaceFileEditStore.setState({ pending: {} }));

async function saveCheckpoint(note = "") {
  fireEvent.click(screen.getByRole("button", { name: "Save checkpoint" }));
  const dialog = await screen.findByRole("alertdialog");
  if (note) {
    fireEvent.change(
      within(dialog).getByRole("textbox", { name: "Checkpoint name" }),
      { target: { value: note } },
    );
  }
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Save checkpoint" }),
  );
}

// The doc-mode surface reads the session's task (to resolve the work_dir) and
// then fetches the file body through the review transport. A hoisted mutable
// mock steers the file content — including the truncation signal — so the test
// can assert the surface reports a truncated preview (with the full size)
// instead of silently cutting the text, and hides the notice when complete.
const session = vi.hoisted(() => ({
  current: {
    files: { workspace_id: "ws-1", attachments: [], tasks: [], repos: [] } as ChatSessionFilesResponse,
    content: {
      text: "hello",
      encoding: "utf8" as const,
      offset: 0,
      next_offset: 5,
      size: 102400,
      has_more: true,
    },
  },
}));

vi.mock("@multica/core/chat/queries", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/chat/queries")>("@multica/core/chat/queries");
  return {
    ...actual,
    chatSessionFilesOptions: (wsId: string, sessionId: string) => ({
      queryKey: ["chat", wsId, "session-files", sessionId] as const,
      queryFn: () => Promise.resolve(session.current.files),
      enabled: !!wsId && !!sessionId,
      staleTime: 15_000,
    }),
  };
});

vi.mock("../../platform/local-review-pages", async () => {
  const actual = await vi.importActual<typeof import("../../platform/local-review-pages")>("../../platform/local-review-pages");
  return {
    ...actual,
    applyReviewFileEdit: vi.fn().mockResolvedValue({ conflict: false }),
    // Only the "file_content" action is exercised for a workspace_file doc; the
    // content versions hooks are disabled (ref.kind !== "workspace_file"), so no
    // other review action is requested here.
    requestReviewPage: vi.fn((input: { relative_path?: string }) =>
      Promise.resolve({ path: input.relative_path ?? ".", content: session.current.content })),
  };
});

// A controllable signed-in user: the Edit affordance is gated on a member (the
// backend record handler also gates on requireUserID only), so the test drives
// it directly rather than booting the whole auth store.
const authState = vi.hoisted(() => ({ user: null as unknown }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (state: { user: unknown }) => unknown) =>
      selector ? selector({ user: authState.user }) : { user: authState.user },
    { getState: () => ({ user: authState.user }) },
  ),
  registerAuthStore: vi.fn(),
  createAuthStore: vi.fn(),
}));

// The content-versions data layer, steered per test so the edit flow can exercise
// success, a 409 conflict, and rollback without a live server.
const cvs = vi.hoisted(() => ({
  current: null as null | Record<string, unknown>,
  versions: [] as unknown[],
  // The record mutation's async fn: default resolves; a test overrides it to
  // reject (409) to drive the conflict path.
  record: null as null | ((input: Record<string, unknown>) => Promise<unknown>),
  delete: null as null | ((input: Record<string, unknown>) => Promise<void>),
}));
vi.mock("@multica/core/chat", async (importOriginal) => ({
  ...await importOriginal<typeof import("@multica/core/chat")>(),
  useDocumentCurrentContent: () => ({
    data: cvs.current ? { ...cvs.current, owner_revision: cvs.current.owner_revision ?? cvs.current.revision } : undefined,
    isLoading: false,
    isError: false,
    refetch: () => {},
  }),
}));
vi.mock("@multica/core/chat/document-content-version-queries", () => ({
  documentContentVersionKeys: {
    all: (wsId: string) => ["document-content-versions", wsId] as const,
    current: (wsId: string, ref: { id: string; kind: string }) =>
      ["document-content-versions", wsId, ref.kind, ref.id, "current"] as const,
    list: (wsId: string, ref: { id: string; kind: string }) =>
      ["document-content-versions", wsId, ref.kind, ref.id, "list"] as const,
    single: (wsId: string, ref: { id: string; kind: string }, revision: number) =>
      ["document-content-versions", wsId, ref.kind, ref.id, revision] as const,
  },
  useDocumentContentVersions: () => ({
    data: cvs.versions,
    isLoading: false,
  }),
  useRecordDocumentContentVersion: () => ({
    mutateAsync: (vars: {
      ref: unknown;
      input: Record<string, unknown>;
    }) =>
      cvs.record ? cvs.record(vars.input) : Promise.resolve(cvs.current),
    isPending: false,
  }),
  useRollbackDocumentContentVersion: () => ({
    mutate: () => Promise.resolve(),
  }),
  useDeleteDocumentContentVersion: () => ({
    mutateAsync: (input: Record<string, unknown>) =>
      cvs.delete ? cvs.delete(input) : Promise.resolve(),
  }),
}));

// CodeMirror owns a contenteditable surface. This harness keeps the test at the
// doc-mode wiring boundary while exercising the same source-editor contract.
vi.mock("../../editor", async () => {
  const actual = await vi.importActual<typeof import("../../editor")>("../../editor");
  return {
    ...actual,
    CodeMirrorEditor: ({
    value,
    label,
    onChange,
  }: {
    value: string;
    label: string;
    onChange: (content: string) => void;
  }) => (
    <div
      data-testid="doc-edit-input"
      role="textbox"
      aria-label={label}
      contentEditable
      suppressContentEditableWarning
      onInput={(event) => onChange(event.currentTarget.textContent ?? "")}
    >
      {value}
    </div>
  ),
  };
});

function editDocument(value: string) {
  const editor = screen.getByTestId("doc-edit-input");
  editor.textContent = value;
  fireEvent.input(editor);
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function openWorkspaceDoc() {
  useDocModeStore.getState().openDoc("session-1", {
    kind: "workspace_file",
    id: "task-1:src/app.ts",
    label: "src/app.ts",
    sessionId: "session-1",
    taskId: "task-1",
    runtimeId: "rt-1",
    relativePath: "src/app.ts",
  });
}

it("previews a project directory file without a run and keeps it read-only", async () => {
  session.current.files = {
    ...session.current.files,
    tasks: [],
    directories: [{ project_resource_id: "resource-1", daemon_id: "daemon-1", runtime_id: "rt-1", path: "/project" }],
  };
  useDocModeStore.getState().openDoc("session-1", {
    kind: "workspace_file", id: "resource:resource-1:README.md", sessionId: "session-1",
    projectResourceId: "resource-1", relativePath: "README.md",
  });

  renderSurface();

  expect(await screen.findByText("hello")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: enChat.doc_mode.edit })).not.toBeInTheDocument();
});

function renderSurface(client = newClient()) {
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <DocModeSurface wsId="ws-1" sessionId="session-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.mocked(applyReviewFileEdit).mockClear();
  Object.defineProperty(window, "desktopAPI", { configurable: true, value: undefined });
  vi.mocked(requestReviewPage).mockImplementation((input) =>
    Promise.resolve({ path: input.relative_path ?? ".", content: session.current.content }),
  );
  cvs.current = null;
  cvs.versions = [];
  cvs.record = null;
  cvs.delete = null;
  authState.user = null;
  useDocModeStore.setState({
    activeBySession: {},
    workingBySession: {},
    diffViewBySession: {},
    draftsByDocument: {},
  });
  session.current = {
    files: {
      workspace_id: "ws-1",
      project_id: "proj-1",
      attachments: [],
      tasks: [
        { id: "task-1", runtime_id: "rt-1", work_dir: "/work", created_at: "2026-01-01" } as never,
      ],
      repos: [],
    },
    content: {
      text: "hello",
      encoding: "utf8",
      offset: 0,
      next_offset: 5,
      size: 102400,
      has_more: true,
    },
  };
});

describe("DocModeSurface (workspace_file body + truncation notice)", () => {
  it("retains text entered while a workspace save is pending and rebases its next save on the submitted draft", async () => {
    session.current.content = { ...session.current.content, has_more: false };
    let finish: () => void = () => { throw new Error("Save not started"); };
    vi.mocked(applyReviewFileEdit).mockImplementationOnce(() => new Promise((resolve) => {
      finish = () => resolve({ file_path: "src/app.ts", merged: true, conflict: false, bytes: 10, new_content: "submitted draft" });
    }));
    openWorkspaceDoc();
    renderSurface();
    fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
    editDocument("submitted draft");
    fireEvent.click(screen.getByRole("button", { name: "Apply to workspace file" }));
    editDocument("newer unpublished draft");

    await act(async () => finish());

    expect(screen.getByTestId("doc-edit-input")).toHaveTextContent("newer unpublished draft");
    expect(Object.values(useDocModeStore.getState().draftsByDocument)).toContainEqual({ kind: "workspace_file", content: "newer unpublished draft", baseContent: "submitted draft" });
  });
  it("reveals the exact body-search occurrence in source and returns to preview", async () => {
    session.current.content = { ...session.current.content, text: "hello\nhello second", has_more: false };
    openWorkspaceDoc();
    const doc = useDocModeStore.getState().activeBySession["session-1"];
    if (!doc) throw new Error("Expected the opened fixture");
    useDocModeStore.getState().openDoc("session-1", doc, { offset: 6, text: "hello", line: 2 });
    const view = renderSurface();
    const mark = await screen.findByText("hello", { selector: "mark" });
    expect(mark.previousSibling?.textContent).toBe("hello\n");
    expect(mark.nextSibling?.textContent).toBe(" second");
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await waitFor(() => expect(view.container.querySelector("mark")).toBeNull());
  });

  it("reports a changed source instead of highlighting a different occurrence", async () => {
    session.current.content = { ...session.current.content, text: "hello\nchanged second", has_more: false };
    openWorkspaceDoc();
    const doc = useDocModeStore.getState().activeBySession["session-1"];
    if (!doc) throw new Error("Expected the opened fixture");
    useDocModeStore.getState().openDoc("session-1", doc, { offset: 6, text: "hello", line: 2 });
    const view = renderSurface();
    expect(await screen.findByRole("alert")).toHaveTextContent(enChat.doc_file_list.content_changed);
    expect(view.container.querySelector("mark")).toBeNull();
  });

  it("renders the file body resolved from the session task's work_dir", async () => {
    openWorkspaceDoc();
    renderSurface();
    // The workspace-file content query resolves through the mocked transport.
    await screen.findByText("hello");
  });

  it("merges against the edit-start body after the workspace file refreshes", async () => {
    session.current.content = {
      ...session.current.content,
      size: 5,
      has_more: false,
    };
    const client = newClient();
    openWorkspaceDoc();
    renderSurface(client);
    fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
    editDocument("my file draft");
    session.current.content = {
      ...session.current.content,
      text: "other writer",
    };
    await act(async () => { await client.invalidateQueries({ queryKey: ["chat-workspace-file-content"] }); });
    act(() => useDocModeStore.getState().setWorking("session-1", true));
    expect(screen.getByTestId("doc-edit-input")).toHaveTextContent("my file draft");
    fireEvent.click(screen.getByRole("button", { name: "Apply to workspace file" }));
    await waitFor(() => expect(applyReviewFileEdit).toHaveBeenCalledWith(expect.objectContaining({
      edit_base: "hello", edit_new: "my file draft",
    })));
  });

  it("shows a truncation notice with the full size when the preview is cut", async () => {
    openWorkspaceDoc();
    renderSurface();
    await screen.findByText("hello");
    // has_more true + size 102400 → "Preview truncated — full file is 100 KB".
    expect(
      screen.getByText(enChat.doc_mode.truncated.replace("{{total}}", "100 KB")),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
  });

  it("offers the original local file when the desktop path bridge is available", async () => {
    const openPath = vi.fn().mockResolvedValue({ ok: true });
    Object.defineProperty(window, "desktopAPI", { configurable: true, value: { openPath } });
    openWorkspaceDoc();
    renderSurface();
    await screen.findByText("hello");
    fireEvent.click(screen.getByRole("button", { name: "Open original file" }));
    expect(openPath).toHaveBeenCalledWith("/work/src/app.ts");
  });

  it("hides the truncation notice when the preview is complete", async () => {
    session.current.content.has_more = false;
    openWorkspaceDoc();
    renderSurface();
    await screen.findByText("hello");
    expect(
      screen.queryByText(enChat.doc_mode.truncated.replace("{{total}}", "100 KB")),
    ).not.toBeInTheDocument();
  });

  it("loads every page before enabling full workspace editing", async () => {
    session.current.content = {
      text: "hello",
      encoding: "utf8",
      offset: 0,
      next_offset: 5,
      size: 11,
      has_more: true,
    };
    vi.mocked(requestReviewPage).mockImplementation(async (input) => {
      const offset = input.action === "file_content" ? input.offset ?? 0 : 0;
      return {
        path: input.relative_path ?? ".",
        content: {
          text: offset === 0 ? "hello" : " world",
          encoding: "utf8",
          offset,
          next_offset: offset === 0 ? 5 : 11,
          size: 11,
          has_more: offset === 0,
        },
      } as Awaited<ReturnType<typeof requestReviewPage>>;
    });
    openWorkspaceDoc();
    renderSurface();
    await screen.findByText("hello");
    fireEvent.click(screen.getByRole("button", { name: "Load full content" }));
    await waitFor(() => {
      expect(vi.mocked(requestReviewPage)).toHaveBeenCalledWith(
        expect.objectContaining({ action: "file_content", offset: 5 }),
      );
    });
    expect(screen.queryByRole("button", { name: "Load full content" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(await screen.findByTestId("doc-edit-input")).toHaveTextContent("hello world");
  });

  it("renders source files with syntax highlighting", async () => {
    session.current.content = {
      text: "const value = 1;",
      encoding: "utf8",
      offset: 0,
      next_offset: 16,
      size: 16,
      has_more: false,
    };
    openWorkspaceDoc();
    renderSurface();
    await waitFor(() => expect(document.querySelector(".hljs-keyword")).not.toBeNull());
  });
});

describe("DocModeSurface (image attachment preview)", () => {
  it.each([null, "https://cdn.example.test/updated.png"])("previews the latest binary version, falling back to the upload when current URL is %s", async (currentUrl) => {
    cvs.current = currentUrl ? { content: null, content_url: currentUrl, content_type: "image/png", revision: 2 } : null;
    session.current.files = {
      ...session.current.files,
      attachments: [{
        id: "att-image",
        workspace_id: "ws-1",
        issue_id: null,
        comment_id: null,
        chat_session_id: "session-1",
        chat_message_id: null,
        uploader_type: "member",
        uploader_id: "u1",
        filename: "board.png",
        url: "https://cdn.example.test/board.png",
        download_url: "https://cdn.example.test/board.png",
        markdown_url: "https://cdn.example.test/board.png",
        content_type: "image/png",
        size_bytes: 128,
        created_at: "2026-01-01",
      }],
    };
    useDocModeStore.getState().openDoc("session-1", {
      kind: "attachment",
      id: "att-image",
      label: "board.png",
      contentType: "image/png",
      sessionId: "session-1",
    });
    const client = newClient();
    renderSurface(client);
    await waitFor(() => expect(client.getQueryData(["chat", "ws-1", "session-files", "session-1"])).toMatchObject({ attachments: [{ id: "att-image" }] }));
    act(() => useDocModeStore.getState().setWorking("session-1", true));
    const image = await screen.findByRole("img", { name: "board.png" });
    expect(image).toHaveAttribute("src", currentUrl ?? "https://cdn.example.test/board.png");
  });
});

describe("DocModeSurface (fast text attachment preview)", () => {
  it("reads text attachments directly instead of waiting for version history", async () => {
    const read = vi.fn().mockResolvedValue({
      text: "# Fast attachment",
      originalContentType: "text/markdown",
    });
    setApiInstance({ getAttachmentTextContent: read } as never);
    useDocModeStore.getState().openDoc("session-1", {
      kind: "attachment",
      id: "att-text",
      label: "REPORT.md",
      contentType: "text/markdown",
      sessionId: "session-1",
    });
    renderSurface();
    await screen.findByText("Fast attachment");
    expect(read).toHaveBeenCalledWith("att-text", expect.any(AbortSignal));
  });
});

describe("DocModeSurface (requirement-doc human edit)", () => {
  beforeEach(() => {
    authState.user = { id: "u1" };
    cvs.versions = [];
    cvs.record = null;
    cvs.delete = null;
    cvs.current = {
      id: "cv-1",
      owner_type: "issue",
      owner_id: "issue-1",
      revision: 1,
      content_type: "text/markdown",
      content: "# Original",
      content_url: null,
      size_bytes: 12,
      actor_type: "member",
      actor_id: "u1",
      merged: false,
      note: null,
      created_at: "2026-01-01",
    };
  });

  function openIssueDoc() {
    useDocModeStore.getState().openDoc("session-1", {
      kind: "issue",
      id: "issue-1",
      label: "Requirement",
      contentType: "text/markdown",
    });
  }

  it("offers an Edit control for a signed-in member's text document", async () => {
    openIssueDoc();
    renderSurface();
    // The Edit affordance is gated on a signed-in member + an issue's text doc.
    await screen.findByText("Edit");
    expect(screen.getByText("Edit")).toBeInTheDocument();
  });

  it("offers source and preview editors for Markdown human edits", async () => {
    openIssueDoc();
    renderSurface();
    fireEvent.click(await screen.findByText("Edit"));
    expect(screen.getByText("Source")).toBeInTheDocument();
    fireEvent.click(screen.getByText("Preview"));
    await waitFor(() => expect(document.querySelector(".ProseMirror")).not.toBeNull());
  });

  it("never offers Edit for a binary (attachment) document", async () => {
    useDocModeStore.getState().openDoc("session-1", {
      kind: "attachment",
      id: "att-1",
      label: "Attachment",
      contentType: "image/png",
    });
    renderSurface();
    // The toolbar always renders; the Edit control does not (gated on kind=issue).
    await screen.findByText("Close");
    expect(screen.queryByText("Edit")).not.toBeInTheDocument();
  });

  it("commits the edit as a new version using the current revision as the optimistic lock", async () => {
    cvs.record = vi.fn(async () => cvs.current);
    openIssueDoc();
    renderSurface();
    fireEvent.click(await screen.findByText("Edit"));
    await screen.findByTestId("doc-edit-input");
    // The editor starts from the current content, not an empty buffer.
    expect(screen.getByTestId("doc-edit-input")).toHaveTextContent("# Original");
    editDocument("# Edited");
    await saveCheckpoint("Reviewed draft");
    await waitFor(() => expect(cvs.record).toHaveBeenCalled());
    expect(cvs.record).toHaveBeenCalledWith({
      content_type: "text/markdown",
      content: "# Edited",
      expected_revision: 1,
      merge: false,
      session_id: "session-1",
      note: "Reviewed draft",
    });
  });

  it("saves with the issue owner revision when it differs from the history revision", async () => {
    cvs.record = vi.fn(async () => cvs.current);
    cvs.current = { ...cvs.current, revision: 1, owner_revision: 8 };
    openIssueDoc();
    renderSurface();
    fireEvent.click(await screen.findByText("Edit"));
    editDocument("# Updated");
    await saveCheckpoint();
    await waitFor(() => expect(cvs.record).toHaveBeenCalledWith({
      content_type: "text/markdown", content: "# Updated", expected_revision: 8, merge: false, session_id: "session-1",
    }));
  });

  it("keeps an unsaved workspace-file draft across close and reopen", async () => {
    session.current.content = {
      ...session.current.content,
      size: 5,
      has_more: false,
    };
    openWorkspaceDoc();
    const view = renderSurface();
    fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
    editDocument("recoverable draft");
    const beforeUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(beforeUnload);
    expect(beforeUnload.defaultPrevented).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByTestId("doc-edit-input")).toHaveTextContent("recoverable draft");
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(useDocModeStore.getState().activeBySession["session-1"]).toBeNull();
    expect(Object.values(useDocModeStore.getState().draftsByDocument)).toContainEqual(
      expect.objectContaining({ content: "recoverable draft", baseContent: "hello" }),
    );

    view.unmount();
    openWorkspaceDoc();
    renderSurface();
    expect(await screen.findByTestId("doc-edit-input")).toHaveTextContent("recoverable draft");
  });

  it("keeps the edit-start revision when a refreshed document arrives before the first save", async () => {
    cvs.record = vi.fn(async () => cvs.current);
    openIssueDoc();
    renderSurface();
    fireEvent.click(await screen.findByText("Edit"));
    editDocument("# My draft");
    cvs.current = { ...cvs.current, revision: 2, content: "# Other writer" };
    act(() => useDocModeStore.getState().setWorking("session-1", true));
    expect(screen.getByTestId("doc-edit-input")).toHaveTextContent("# My draft");
    await saveCheckpoint();
    await waitFor(() => expect(cvs.record).toHaveBeenCalledWith({
      content_type: "text/markdown", content: "# My draft", expected_revision: 1, merge: false, session_id: "session-1",
    }));
  });

  it("renders additions and removals for the selected historical document", async () => {
    cvs.current = { ...cvs.current, revision: 3, content: "common\ncurrent line" };
    cvs.versions = [cvs.current, { ...cvs.current, id: "cv-2", revision: 2, content: "common\nprevious line" }, { ...cvs.current, id: "cv-1", revision: 1, content: "common\nfirst line" }];
    openIssueDoc();
    renderSurface();
    fireEvent.click(await screen.findByRole("button", { name: "History" }));
    fireEvent.click(await screen.findByRole("button", { name: "Diff" }));
    expect(screen.getByText(/previous line/)).toHaveTextContent("- previous line");
    expect(
      screen.getAllByText(/current line/).find((element) =>
        element.textContent?.includes("+ current line")
      ),
    ).toHaveTextContent("+ current line");
    fireEvent.click(screen.getByRole("button", { name: /^v1/ }));
    fireEvent.click(screen.getByRole("button", { name: "Diff" }));
    expect(screen.queryByText(/previous line/)).not.toBeInTheDocument();
    expect(
      screen.getAllByLabelText("Diff").find((element) => element.tagName === "PRE"),
    ).toHaveTextContent("- first line");
  });

  it("confirms before deleting an older named checkpoint", async () => {
    cvs.delete = vi.fn().mockResolvedValue(undefined);
    cvs.current = { ...cvs.current, revision: 3, content: "current" };
    cvs.versions = [
      cvs.current,
      { ...cvs.current, id: "cv-2", revision: 2, content: "older named version" },
      { ...cvs.current, id: "cv-1", revision: 1, content: "autosave", merged: true },
    ];
    openIssueDoc();
    renderSurface();
    fireEvent.click(await screen.findByRole("button", { name: "History" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete checkpoint 2" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/Checkpoint 2 will be removed/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete checkpoint" }));
    await waitFor(() => expect(cvs.delete).toHaveBeenCalledWith({
      ref: expect.objectContaining({ id: "issue-1", kind: "issue" }),
      revision: 2,
    }));
  });

  it("surfaces a 409 conflict as a reload prompt without clobbering the draft", async () => {
    cvs.record = vi.fn(async () => {
      throw new ApiError("conflict", 409, "Conflict");
    });
    openIssueDoc();
    renderSurface();
    fireEvent.click(await screen.findByText("Edit"));
    await screen.findByTestId("doc-edit-input");
    editDocument("# Edited");
    await saveCheckpoint();
    // A 409 keeps the draft open and prompts the user to reload (fail-closed).
    await screen.findByText(enChat.doc_mode.conflict_title);
    expect(screen.getByTestId("doc-edit-input")).toHaveTextContent("# Edited");
    fireEvent.click(screen.getByRole("button", { name: "Keep editing" }));
    expect(screen.getByRole("button", { name: "Save checkpoint" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Save checkpoint" }));
    expect(cvs.record).toHaveBeenCalledTimes(1);
  });

  it("resolves a conflict block and saves the merged result", async () => {
    let attempts = 0;
    cvs.record = vi.fn(async () => {
      attempts += 1;
      if (attempts === 1) throw new ApiError("conflict", 409, "Conflict");
      return {
        ...cvs.current,
        revision: 2,
        owner_revision: 2,
        content: "# Local",
      };
    });
    openIssueDoc();
    renderSurface();
    fireEvent.click(await screen.findByText("Edit"));
    editDocument("# Local");
    cvs.current = {
      ...cvs.current,
      revision: 2,
      owner_revision: 2,
      content: "# Remote",
    };
    await saveCheckpoint("Reviewed draft");
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Conflict")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Use my block" }));
    expect(within(dialog).getByRole("button", { name: "Save merged" })).toBeEnabled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Save merged" }));
    await waitFor(() => expect(cvs.record).toHaveBeenCalledTimes(2));
    expect(cvs.record).toHaveBeenLastCalledWith({
      content_type: "text/markdown",
      content: "# Local",
      expected_revision: 2,
      merge: false,
      session_id: "session-1",
      note: "Resolved concurrent edits",
    });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  });
});
