// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import enCommon from "../../locales/en/common.json";
import enChat from "../../locales/en/chat.json";
import { useDocModeStore } from "@multica/core/chat";
import type { ChatSessionFilesResponse } from "@multica/core/types";
import { DocModeSurface } from "./doc-mode-surface";

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
