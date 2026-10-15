// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import enCommon from "../../locales/en/common.json";
import enChat from "../../locales/en/chat.json";
import { useDocModeStore } from "@multica/core/chat";
import type { ChatSessionFilesResponse } from "@multica/core/types";
import { DocModeSurface } from "./doc-mode-surface";
import { ApiError, setApiInstance } from "@multica/core/api";

const TEST_RESOURCES = { en: { common: enCommon, chat: enChat } };

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
    // Only the "file_content" action is exercised for a workspace_file doc; the
    // content versions hooks are disabled (ref.kind !== "workspace_file"), so no
    // other review action is requested here.
    requestReviewPage: (input: { relative_path?: string }) =>
      Promise.resolve({ path: input.relative_path ?? ".", content: session.current.content }),
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
  useDocumentContentVersionCurrent: () => ({
    data: cvs.current,
    isLoading: false,
    isError: false,
    refetch: () => {},
  }),
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
}));

// FileViewer is the shared skill-page editor; stub it so the suite asserts the
// doc-mode edit wiring (not the editor internals) and needs no "skills" i18n.
vi.mock("../../skills/components/file-viewer", () => ({
  FileViewer: ({
    content,
    onChange,
  }: {
    content: string;
    onChange: (content: string) => void;
  }) => (
    <textarea
      data-testid="doc-edit-input"
      value={content}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
  isMarkdownPath: (path: string) => path.endsWith(".md") || path.endsWith(".mdx"),
}));

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function openWorkspaceDoc() {
  useDocModeStore.getState().openDoc("ws-1", {
    kind: "workspace_file",
    id: "task-1:src/app.ts",
    label: "src/app.ts",
    sessionId: "session-1",
    taskId: "task-1",
    runtimeId: "rt-1",
    relativePath: "src/app.ts",
  });
}

function renderSurface() {
  return render(
    <QueryClientProvider client={newClient()}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <DocModeSurface wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  useDocModeStore.setState({
    activeByWorkspace: {},
    workingByWorkspace: {},
    diffViewByWorkspace: {},
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
  it("renders the file body resolved from the session task's work_dir", async () => {
    openWorkspaceDoc();
    renderSurface();
    // The workspace-file content query resolves through the mocked transport.
    await screen.findByText("hello");
  });

  it("shows a truncation notice with the full size when the preview is cut", async () => {
    openWorkspaceDoc();
    renderSurface();
    await screen.findByText("hello");
    // has_more true + size 102400 → "Preview truncated — full file is 100 KB".
    expect(
      screen.getByText(enChat.doc_mode.truncated.replace("{{total}}", "100 KB")),
    ).toBeInTheDocument();
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
});

describe("DocModeSurface (fast text attachment preview)", () => {
  it("reads text attachments directly instead of waiting for version history", async () => {
    const read = vi.fn().mockResolvedValue({
      text: "# Fast attachment",
      originalContentType: "text/markdown",
    });
    setApiInstance({ getAttachmentTextContent: read } as never);
    useDocModeStore.getState().openDoc("ws-1", {
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
    useDocModeStore.getState().openDoc("ws-1", {
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

  it("never offers Edit for a binary (attachment) document", async () => {
    useDocModeStore.getState().openDoc("ws-1", {
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
    expect(screen.getByTestId("doc-edit-input")).toHaveValue("# Original");
    fireEvent.change(screen.getByTestId("doc-edit-input"), {
      target: { value: "# Edited" },
    });
    fireEvent.click(screen.getByText("Save"));
    await waitFor(() => expect(cvs.record).toHaveBeenCalled());
    expect(cvs.record).toHaveBeenCalledWith({
      content_type: "text/markdown",
      content: "# Edited",
      expected_revision: 1,
    });
  });

  it("surfaces a 409 conflict as a reload prompt without clobbering the draft", async () => {
    cvs.record = vi.fn(async () => {
      throw new ApiError("conflict", 409, "Conflict");
    });
    openIssueDoc();
    renderSurface();
    fireEvent.click(await screen.findByText("Edit"));
    await screen.findByTestId("doc-edit-input");
    fireEvent.change(screen.getByTestId("doc-edit-input"), {
      target: { value: "# Edited" },
    });
    fireEvent.click(screen.getByText("Save"));
    // A 409 keeps the draft open and prompts the user to reload (fail-closed).
    await screen.findByText(enChat.doc_mode.changed_reload);
  });
});
