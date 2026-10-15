// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import enCommon from "../../locales/en/common.json";
import enChat from "../../locales/en/chat.json";
import { useDocModeStore } from "@multica/core/chat";
import type { ChatSessionFilesResponse } from "@multica/core/types";
import { DocModeFileList } from "./doc-file-list";

const TEST_RESOURCES = { en: { common: enCommon, chat: enChat } };

// The file list reads the chat session's server projection (not client recent
// context). A hoisted mutable mock steers the session files, the project
// title, the workspace files the review transport returns, and the project's
// configured repos, so the same list renders after a refresh or on another
// device (which would not carry client recent context).
const session = vi.hoisted(() => ({
  current: {
    files: { workspace_id: "ws-1", attachments: [], tasks: [], repos: [] } as ChatSessionFilesResponse,
    projectTitle: "",
    hasProject: false,
    entries: [] as Array<{ path: string; size: number; directory: boolean }>,
    entriesByPath: {} as Record<string, Array<{ path: string; size: number; directory: boolean }>>,
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

vi.mock("@multica/core/projects/queries", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/projects/queries")>("@multica/core/projects/queries");
  return {
    ...actual,
    projectDetailOptions: (wsId: string, id: string) => ({
      queryKey: ["projects", wsId, "detail", id] as const,
      queryFn: () =>
        Promise.resolve({ id, workspace_id: wsId, title: session.current.projectTitle } as never),
    }),
  };
});

vi.mock("../../platform/local-review-pages", async () => {
  const actual = await vi.importActual<typeof import("../../platform/local-review-pages")>("../../platform/local-review-pages");
  return {
    ...actual,
    requestReviewPage: (request: { path?: string }) => Promise.resolve({ path: ".", entries: session.current.entriesByPath[request.path ?? ""] ?? session.current.entries }),
  };
});

vi.mock("../../issues/components/local-review-index", () => ({
  LocalReviewIndex: () => <div data-testid="local-review-index">Source control</div>,
}));

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function renderList(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <DocModeFileList wsId="ws-1" sessionId="session-1" />
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
      attachments: [
        { id: "att-1", filename: "report.pdf", url: "", content_type: "application/pdf" } as never,
      ],
      tasks: [
        { id: "task-1", runtime_id: "rt-1", work_dir: "/work", created_at: "2026-01-01" } as never,
      ],
      repos: [],
    },
    projectTitle: "Acme",
    hasProject: true,
    entries: [{ path: "src/app.ts", size: 10, directory: false }],
    entriesByPath: {},
  };
});

describe("DocModeFileList (session-driven, doc-mode entry B)", () => {
  it("lists the session's project, attachments, and workspace files", async () => {
    renderList(newClient());
    // The project is the section header (no longer dropped) and the session's
    // attachments + task-workdir files are the openable rows.
    await screen.findByText("Acme");
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(screen.getByText("src")).toBeInTheDocument();
    expect(screen.queryByText("app.ts")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("src"));
    expect(screen.getByText("app.ts")).toBeInTheDocument();
  });

  it("searches the full tree and opens matching ancestor folders", async () => {
    renderList(newClient());
    await screen.findByText("Acme");
    expect(screen.queryByText("app.ts")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(enChat.doc_file_list.filter_placeholder), {
      target: { value: "app.ts" },
    });
    expect(screen.getByText("app.ts")).toBeInTheDocument();
  });

  it("opens the clicked attachment through openDoc", async () => {
    renderList(newClient());
    fireEvent.click(await screen.findByText("report.pdf"));
    const active = useDocModeStore.getState().activeByWorkspace["ws-1"];
    expect(active).toMatchObject({ kind: "attachment", id: "att-1" });
  });

  it("opens the clicked workspace file through openDoc", async () => {
    renderList(newClient());
    fireEvent.click(await screen.findByText("src"));
    fireEvent.click(await screen.findByText("app.ts"));
    const active = useDocModeStore.getState().activeByWorkspace["ws-1"];
    expect(active).toMatchObject({ kind: "workspace_file", id: expect.stringContaining("src/app.ts") });
  });

  it("filters documents by the filter box (client-side)", async () => {
    renderList(newClient());
    await screen.findByText("report.pdf");
    fireEvent.change(screen.getByLabelText(enChat.doc_file_list.filter_placeholder), {
      target: { value: "report" },
    });
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(screen.queryByText("app.ts")).not.toBeInTheDocument();
  });

  it("shows a no-match state when the filter excludes everything", async () => {
    renderList(newClient());
    await screen.findByText("report.pdf");
    fireEvent.change(screen.getByLabelText(enChat.doc_file_list.filter_placeholder), {
      target: { value: "zzz-nothing" },
    });
    expect(screen.getByText(enChat.doc_file_list.no_match)).toBeInTheDocument();
  });

  it("lists the project's configured repos when there is no task worktree", async () => {
    // No task worktree entries, but the project configures a repo.
    session.current.entries = [];
    session.current.files = {
      ...session.current.files,
      tasks: [],
      repos: [
        { url: "https://github.com/acme/widgets.git", description: "", ref: "main" } as never,
      ],
    };
    renderList(newClient());
    // The repo shows as a reference row (not openable), labelled by owner/repo.
    await screen.findByText("Acme");
    expect(screen.getByText("acme/widgets")).toBeInTheDocument();
    // The attachment is a session file, so it still shows even without a
    // worktree; the worktree file (src/app.ts) is gone.
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(screen.queryByText("app.ts")).not.toBeInTheDocument();
  });

  it("keeps the list after a refresh (server data, not recent context)", async () => {
    const queryClient = newClient();
    const first = renderList(queryClient);
    await screen.findByText("report.pdf");
    // Simulate a refresh: unmount then remount against the same cached query.
    first.unmount();
    renderList(queryClient);
    expect(screen.getByText("Acme")).toBeInTheDocument();
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    fireEvent.click(screen.getByText("src"));
    expect(screen.getByText("app.ts")).toBeInTheDocument();
  });

  it("keeps the file tree and source control on the lead checkout", async () => {
    session.current.files = {
      ...session.current.files,
      tasks: [
        { id: "lead", runtime_id: "rt-1", work_dir: "/lead", created_at: "2026-01-01" } as never,
        { id: "sibling", runtime_id: "rt-1", work_dir: "/sibling", created_at: "2026-01-02" } as never,
      ],
    };
    session.current.entries = [];
    session.current.entriesByPath = {
      "/lead": [{ path: "src/app.ts", size: 10, directory: false }],
      "/sibling": [{ path: "src/app.ts", size: 20, directory: false }],
    };
    renderList(newClient());
    fireEvent.click(await screen.findByText("src"));
    fireEvent.click(await screen.findByText("app.ts"));
    expect(useDocModeStore.getState().activeByWorkspace["ws-1"]).toMatchObject({
      kind: "workspace_file",
      workDir: "/lead",
    });
  });
});
