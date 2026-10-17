// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import enCommon from "../../locales/en/common.json";
import enChat from "../../locales/en/chat.json";
import { useDocModeStore } from "@multica/core/chat";
import { useLocalDirectoryStore } from "@multica/core/chat/local-directory-store";
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
    workspaceErrorByPath: {} as Record<string, Error>,
    workspaceError: null as Error | null,
    error: null as Error | null,
  },
}));

vi.mock("@multica/core/chat/queries", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/chat/queries")>("@multica/core/chat/queries");
  return {
    ...actual,
    chatSessionFilesOptions: (wsId: string, sessionId: string) => ({
      queryKey: ["chat", wsId, "session-files", sessionId] as const,
      queryFn: () => session.current.error ? Promise.reject(session.current.error) : Promise.resolve(session.current.files),
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
    requestReviewPage: (request: { path?: string }) => session.current.workspaceErrorByPath[request.path ?? ""] ?? session.current.workspaceError
      ? Promise.reject(session.current.workspaceErrorByPath[request.path ?? ""] ?? session.current.workspaceError)
      : Promise.resolve({ path: ".", entries: session.current.entriesByPath[request.path ?? ""] ?? session.current.entries }),
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
    activeBySession: {},
    workingBySession: {},
    diffViewBySession: {},
  });
  useLocalDirectoryStore.setState({ currentByWorkspace: {} });
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
    workspaceErrorByPath: {},
    workspaceError: null,
    error: null,
  };
});

describe("DocModeFileList (session-driven, doc-mode entry B)", () => {
  it("lists workspace files in the files tab and attachments in their own tab", async () => {
    renderList(newClient());
    // The project is the section header and the task-workdir (source) files are
    // the openable rows in the files tab; attachments are not mixed in.
    await screen.findByText("Acme");
    expect(screen.getByText("src")).toBeInTheDocument();
    expect(screen.queryByText("report.pdf")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("src"));
    expect(screen.getByText("app.ts")).toBeInTheDocument();
    // The attachment is only in the "Attachments" tab, not the files tab.
    fireEvent.click(screen.getByText(enChat.doc_file_list.attachments));
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
  });

  it("shows a retry state when the session file projection fails", async () => {
    session.current.error = new Error("request failed");
    renderList(newClient());

    expect(await screen.findByRole("alert")).toHaveTextContent(enChat.doc_file_list.load_failed);
    session.current.error = null;
    fireEvent.click(screen.getByRole("button", { name: enChat.doc_file_list.retry }));
    expect(await screen.findByText("Acme")).toBeInTheDocument();
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

  it("opens the clicked attachment (from the attachments tab) through openDoc", async () => {
    renderList(newClient());
    // Wait for the panel to load, then switch to the attachments tab.
    await screen.findByText("Acme");
    // Attachments live in their own tab, not the files tab.
    fireEvent.click(screen.getByText(enChat.doc_file_list.attachments));
    fireEvent.click(await screen.findByText("report.pdf"));
    const active = useDocModeStore.getState().activeBySession["session-1"];
    expect(active).toMatchObject({ kind: "attachment", id: "att-1" });
  });

  it("opens the clicked workspace file through openDoc", async () => {
    renderList(newClient());
    fireEvent.click(await screen.findByText("src"));
    fireEvent.click(await screen.findByText("app.ts"));
    const active = useDocModeStore.getState().activeBySession["session-1"];
    expect(active).toMatchObject({ kind: "workspace_file", id: expect.stringContaining("src/app.ts") });
  });

  it("filters documents by the filter box (client-side)", async () => {
    renderList(newClient());
    await screen.findByText("Acme");
    // The files tab is source only, so the attachment is excluded from the tree;
    // filtering the source file keeps it and its ancestor folder.
    fireEvent.change(screen.getByLabelText(enChat.doc_file_list.filter_placeholder), {
      target: { value: "app.ts" },
    });
    expect(screen.getByText("app.ts")).toBeInTheDocument();
    expect(screen.queryByText("report.pdf")).not.toBeInTheDocument();
  });

  it("shows a no-match state when the filter excludes everything", async () => {
    renderList(newClient());
    // Wait for the source (files) tab to load before filtering.
    await screen.findByText("Acme");
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
    // The files tab has no source (worktree) file, so app.ts is gone; the
    // attachment lives in its own tab, not the files tab.
    expect(screen.queryByText("app.ts")).not.toBeInTheDocument();
    expect(screen.queryByText("report.pdf")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText(enChat.doc_file_list.attachments));
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
  });

  it("falls back to the project repository when a historical worktree is unavailable", async () => {
    session.current.workspaceError = new Error("directory has no matching runtime task binding");
    session.current.files = {
      ...session.current.files,
      repos: [{ url: "https://github.com/acme/widgets.git", description: "", ref: "main" } as never],
    };
    renderList(newClient());

    await screen.findByText("Acme");
    expect(screen.queryByText(enChat.doc_file_list.load_failed)).not.toBeInTheDocument();
    expect(screen.getByText("acme/widgets")).toBeInTheDocument();
  });

  it("keeps the list after a refresh (server data, not recent context)", async () => {
    const queryClient = newClient();
    const first = renderList(queryClient);
    await screen.findByText("Acme");
    // Simulate a refresh: unmount then remount against the same cached query.
    first.unmount();
    renderList(queryClient);
    expect(screen.getByText("Acme")).toBeInTheDocument();
    fireEvent.click(screen.getByText("src"));
    expect(screen.getByText("app.ts")).toBeInTheDocument();
    // The attachment persists in its own tab across the refresh.
    fireEvent.click(screen.getByText(enChat.doc_file_list.attachments));
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
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
    expect(useDocModeStore.getState().activeBySession["session-1"]).toMatchObject({
      kind: "workspace_file",
      workDir: "/lead",
    });
  });

  it("skips a stale lead binding and uses the next live checkout", async () => {
    session.current.files = {
      ...session.current.files,
      tasks: [
        { id: "stale", runtime_id: "rt-old", work_dir: "/stale", created_at: "2026-01-01" } as never,
        { id: "live", runtime_id: "rt-live", work_dir: "/live", created_at: "2026-01-02" } as never,
      ],
    };
    session.current.entries = [];
    session.current.entriesByPath = {
      "/live": [{ path: "src/live.ts", size: 20, directory: false }],
    };
    session.current.workspaceErrorByPath = {
      "/stale": new Error("directory has no matching runtime task binding"),
    };
    renderList(newClient());

    fireEvent.click(await screen.findByText("src"));
    fireEvent.click(await screen.findByText("live.ts"));
    expect(useDocModeStore.getState().activeBySession["session-1"]).toMatchObject({
      kind: "workspace_file",
      workDir: "/live",
    });
  });

  it("does not infer Git from a chat task before a directory is opened", async () => {
    session.current.files = {
      ...session.current.files,
      tasks: [
        { id: "task-1", runtime_id: "rt-1", work_dir: "/work", created_at: "2026-01-01" } as never,
      ],
    };
    renderList(newClient());
    await screen.findByText("Acme");
    expect(screen.queryByTestId("local-review-index")).not.toBeInTheDocument();
  });

  it("keeps Git bound to the current local directory when it belongs to another chat", async () => {
    session.current.files = {
      ...session.current.files,
      tasks: [],
    };
    useLocalDirectoryStore.setState({
      currentByWorkspace: {
        "ws-1": {
          path: "/Users/me/other-project",
          taskId: "task-from-other-chat",
          runtimeId: "runtime-local",
          workspaceId: "ws-1",
        },
      },
    });
    renderList(newClient());
    expect(await screen.findByTestId("local-review-index")).toBeInTheDocument();
  });
});
