// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import enCommon from "../../locales/en/common.json";
import enChat from "../../locales/en/chat.json";
import { useDocModeStore } from "@multica/core/chat";
import { useLocalDirectoryStore } from "@multica/core/chat/local-directory-store";
import type { ChatSessionFilesResponse, Issue } from "@multica/core/types";
import { onIssueUpdated } from "@multica/core/issues/ws-updaters";
import { NavigationProvider } from "../../navigation";
import { DocModeFileList } from "./doc-file-list";

const TEST_RESOURCES = { en: { common: enCommon, chat: enChat } };
const indexEvents = vi.hoisted(() => ({ onChanged: () => {} }));
const listIssues = vi.hoisted(() => vi.fn<typeof import("@multica/core/api").api.listIssues>());

vi.mock("@multica/core/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@multica/core/api")>(),
  api: { listIssues },
}));

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
    shallow: false,
    errorsByRelativePath: {} as Record<string, Error>,
    entriesByRelativePath: {} as Record<string, Array<{ path: string; size: number; directory: boolean }>>,
    directoryRequests: [] as string[],
    pageEntries: {} as Record<string, {entries: Array<{path:string; size:number; directory:boolean}>; next_offset:number; has_more:boolean}>,
    pageRequests: [] as string[],
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
    requestReviewPage: (request: { path?: string; relative_path?: string; offset?: number }) => {
      if (session.current.shallow) {
        const relative = request.relative_path ?? ".";
        session.current.directoryRequests.push(relative);
        const key = `${relative}:${request.offset ?? 0}`;
        session.current.pageRequests.push(key);
        const page = session.current.pageEntries[key];
        if(page) return Promise.resolve({path:request.path ?? ".",recursive:false,truncated:false,...page});
        if (session.current.errorsByRelativePath[relative]) return Promise.reject(session.current.errorsByRelativePath[relative]);
        return Promise.resolve({path:request.path ?? ".",recursive:false,entries:session.current.entriesByRelativePath[relative] ?? [],next_offset:2,has_more:false,truncated:false});
      }
      return session.current.workspaceErrorByPath[request.path ?? ""] ?? session.current.workspaceError
      ? Promise.reject(session.current.workspaceErrorByPath[request.path ?? ""] ?? session.current.workspaceError)
      : Promise.resolve({ path: request.path ?? ".", entries: session.current.entriesByPath[request.path ?? ""] ?? session.current.entries });
    },
  };
});

vi.mock("../../issues/components/local-review-index", () => ({
  LocalReviewIndex: ({onChanged}:{onChanged:()=>void}) => {indexEvents.onChanged=onChanged;return <div data-testid="local-review-index">Source control</div>;},
}));

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function renderList(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <NavigationProvider value={{
          push: vi.fn(), replace: vi.fn(), back: vi.fn(), pathname: "/chat",
          searchParams: new URLSearchParams(), hash: "", getShareableUrl: (path) => path,
        }}>
          <DocModeFileList wsId="ws-1" sessionId="session-1" />
        </NavigationProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  listIssues.mockReset().mockResolvedValue({ issues: [], total: 0 });
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
    pageEntries: {}, pageRequests: [], shallow: false, entriesByRelativePath: {}, directoryRequests: [], errorsByRelativePath: {},
    workspaceErrorByPath: {},
    workspaceError: null,
    error: null,
  };
});

describe("DocModeFileList (session-driven, doc-mode entry B)", () => {


  it("loads one page at a time for both the root and expanded folders", async () => {
    session.current.shallow = true;
    session.current.pageEntries = {
      ".:0": {entries:[{path:"docs",directory:true,size:0}],next_offset:100,has_more:true},
      ".:100": {entries:[{path:"last.txt",directory:false,size:0}],next_offset:101,has_more:false},
      "docs:0": {entries:[{path:"docs/first.md",directory:false,size:0}],next_offset:100,has_more:true},
      "docs:100": {entries:[{path:"docs/last.md",directory:false,size:0}],next_offset:101,has_more:false},
    };
    renderList(newClient());
    await screen.findByText("docs");
    expect(session.current.pageRequests).toEqual([".:0"]);
    fireEvent.click(screen.getByRole("button",{name:enChat.doc_file_list.load_more}));
    await screen.findByText("last.txt");
    fireEvent.click(screen.getByRole("button",{name:"docs"}));
    await screen.findByText("first.md");
    expect(session.current.pageRequests).toEqual([".:0", ".:100", "docs:0"]);
    fireEvent.click(screen.getByRole("button",{name:enChat.doc_file_list.load_more}));
    await screen.findByText("last.md");
    expect(session.current.pageRequests).toEqual([".:0", ".:100", "docs:0", "docs:100"]);
  });

  it("searches unopened folders and later pages progressively until complete", async () => {
    session.current.shallow = true;
    session.current.pageEntries = {
      ".:0": {entries:[{path:"first-needle.md",directory:false,size:0}],next_offset:100,has_more:true},
      ".:100": {entries:[{path:"docs",directory:true,size:0}],next_offset:101,has_more:false},
      "docs:0": {entries:[{path:"docs/deep",directory:true,size:0}],next_offset:1,has_more:false},
      "docs/deep:0": {entries:[{path:"docs/deep/last-needle.md",directory:false,size:0}],next_offset:1,has_more:false},
    };
    renderList(newClient());
    await screen.findByText("first-needle.md");
    fireEvent.change(screen.getByRole("textbox"),{target:{value:"needle"}});
    await screen.findByText("first-needle.md");
    expect(screen.queryByText("last-needle.md")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(enChat.doc_file_list.searching);
    await screen.findByText("last-needle.md");
    await waitFor(()=>expect(screen.queryByRole("status")).not.toBeInTheDocument());
    expect(session.current.pageRequests).toEqual([".:0", ".:0", ".:100", "docs:0", "docs/deep:0"]);
    fireEvent.click(screen.getByRole("button",{name:"docs"}));
    expect(screen.queryByText("last-needle.md")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button",{name:"docs"}));
    expect(screen.getByText("last-needle.md")).toBeInTheDocument();
  });

  it("allows collapsing matching ancestors while a search remains active", async () => {
    renderList(newClient());
    await screen.findByText("src");
    fireEvent.change(screen.getByRole("textbox"), {target:{value:"app"}});
    expect(await screen.findByText("app.ts")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", {name:"src"}));
    expect(screen.getByRole("button", {name:"src"})).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("app.ts")).not.toBeInTheDocument();
  });

  it("refreshes an expanded subtree after a Git operation changes its files",async()=>{
    session.current.files.directories=[{project_resource_id:"resource",daemon_id:"daemon",runtime_id:"rt-1",path:"/project",label:"Project directory"}];
    session.current.shallow=true;
    session.current.entriesByRelativePath={".":[{path:"docs",size:0,directory:true}],"docs":[{path:"docs/old.md",size:1,directory:false}]};
    renderList(newClient());
    await userEvent.click(await screen.findByRole("button",{name:"docs"}));
    await screen.findByRole("button",{name:"old.md"});
    session.current.entriesByRelativePath.docs=[{path:"docs/new.md",size:1,directory:false}];
    act(()=>indexEvents.onChanged());
    expect(await screen.findByRole("button",{name:"new.md"})).toBeInTheDocument();
    expect(screen.queryByRole("button",{name:"old.md"})).not.toBeInTheDocument();
  });
  it("keeps sibling folders usable when a child listing fails and retries in place",async()=>{
    session.current.shallow=true;
    session.current.entriesByRelativePath={".":[{path:"broken",size:0,directory:true},{path:"sibling",size:0,directory:true}],"broken":[{path:"broken/fixed.md",size:1,directory:false}]};
    session.current.errorsByRelativePath={broken:new Error("temporary failure")};
    renderList(newClient());
    await userEvent.click(await screen.findByRole("button",{name:"broken"}));
    expect(await screen.findByRole("alert")).toHaveTextContent(enChat.doc_file_list.load_failed);
    expect(screen.getByRole("button",{name:"sibling"})).toBeInTheDocument();
    session.current.errorsByRelativePath={};
    await userEvent.click(screen.getByRole("button",{name:enChat.doc_file_list.retry}));
    expect(await screen.findByRole("button",{name:"fixed.md"})).toBeInTheDocument();
  });
  it("shows empty folders and reads only the folder the user expands",async()=>{
    session.current.shallow=true;
    session.current.entriesByRelativePath={
      ".":[{path:"00-项目管理",size:0,directory:true},{path:"空目录",size:0,directory:true}],
      "00-项目管理":[{path:"00-项目管理/计划.md",size:1,directory:false}],
    };
    renderList(newClient());
    const folder=await screen.findByRole("button",{name:"00-项目管理"});
    expect(screen.getByRole("button",{name:"空目录"})).toBeInTheDocument();
    expect(session.current.directoryRequests).toEqual(["."]);
    await userEvent.click(folder);
    expect(await screen.findByRole("button",{name:"计划.md"})).toBeInTheDocument();
    expect(session.current.directoryRequests).toEqual([".","00-项目管理"]);
  });

  it("requests open project issues and removes a blocked issue when it completes", async () => {
    // Given a completed issue and a blocked issue in the selected project.
    const blocked: Issue = {
      id: "blocked", workspace_id: "ws-1", project_id: "proj-1", number: 1,
      identifier: "TEST-1", title: "Awaiting input", description: null,
      status: "blocked", status_category: "started", priority: "none",
      assignee_type: null, assignee_id: null, creator_type: "member", creator_id: "member-1",
      parent_issue_id: null, position: 1, stage: null, start_date: null, due_date: null,
      metadata: {}, properties: {}, created_at: "2026-01-01", updated_at: "2026-01-01",
    };
    const completed: Issue = { ...blocked, id: "done", identifier: "TEST-2", title: "Finished work", status: "done", status_category: "done" };
    let openIssues = [blocked];
    listIssues.mockImplementation(async (params) => {
      const issues = params?.open_only ? openIssues : [completed, blocked];
      return { issues, total: issues.length };
    });
    const client = newClient();
    renderList(client);
    await screen.findByText("Acme");
    expect(listIssues).not.toHaveBeenCalled();

    // When the issues tab opens, filtering happens on the server before paging.
    fireEvent.click(screen.getByRole("tab", { name: enChat.doc_file_list.issues }));
    expect(await screen.findByText(blocked.title)).toBeInTheDocument();
    expect(listIssues).toHaveBeenCalledWith(expect.objectContaining({ project_id: "proj-1", open_only: true }));
    expect(screen.queryByText(completed.title)).not.toBeInTheDocument();

    // Then an authoritative completion event refreshes this project projection.
    openIssues = [];
    act(() => onIssueUpdated(client, "ws-1", { ...blocked, status: "done", status_category: "done" }));
    await waitFor(() => expect(screen.queryByText(blocked.title)).not.toBeInTheDocument());
    expect(screen.getByText(enChat.doc_file_list.no_issues)).toBeInTheDocument();
  });

  it("keeps an unavailable project's directory visible and offers reconnect recovery", async () => {
    session.current.files = {
      ...session.current.files,
      tasks: [],
      directories: [{ project_resource_id: "resource-1", daemon_id: "daemon-1", path: "/offline-project" }],
      repos: [{ url: "https://github.com/other/repository" }],
    };
    renderList(newClient());

    expect(await screen.findByText("/offline-project")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(enChat.doc_file_list.directory_unavailable);
    expect(screen.getByRole("button", { name: enChat.doc_file_list.retry })).toBeEnabled();
    expect(screen.queryByText("other/repository")).not.toBeInTheDocument();
  });

  it("opens a project-bound local directory before any run exists", async () => {
    session.current.files = {
      ...session.current.files,
      tasks: [],
      directories: [{ project_resource_id: "resource-1", daemon_id: "daemon-1", runtime_id: "rt-1", path: "/project", label: "Project directory" }],
    };
    renderList(newClient());

    fireEvent.click(await screen.findByText("src"));
    fireEvent.click(screen.getByText("app.ts"));

    expect(useDocModeStore.getState().activeBySession["session-1"]).toMatchObject({
      kind: "workspace_file", projectResourceId: "resource-1", workDir: "/project", relativePath: "src/app.ts",
    });
  });

  it("offers both project directories and chat/project worktrees for selection", async () => {
    session.current.files = {
      ...session.current.files,
      directories: [{ project_resource_id: "resource-1", daemon_id: "daemon-1", runtime_id: "rt-1", path: "/project" }],
      project_tasks: [{ id: "project-task", runtime_id: "rt-1", work_dir: "/project-worktree", created_at: "2026-01-01" }],
    };
    session.current.entriesByPath = {
      "/project": [{ path: "local.md", size: 5, directory: false }],
      "/project-worktree": [{ path: "worktree.md", size: 5, directory: false }],
    };
    const user = userEvent.setup();
    renderList(newClient());
    await screen.findByText("local.md");
    await user.click(screen.getByRole("combobox", { name: "Directory" }));
    expect(await screen.findByRole("option", { name: /\/work$/ })).toBeInTheDocument();
    await user.click(screen.getByRole("option", { name: /\/project-worktree$/ }));

    expect(await screen.findByText("worktree.md")).toBeInTheDocument();
    expect(screen.queryByText("local.md")).not.toBeInTheDocument();
    expect(useLocalDirectoryStore.getState().currentByWorkspace["ws-1"]?.path).toBe("/project-worktree");
  });

  it("lists workspace files in the files tab and attachments in their own tab", async () => {
    renderList(newClient());
    // The project is the section header and the task-workdir (source) files are
    // the openable rows in the files tab; attachments are not mixed in.
    await screen.findByText("Acme");
    expect(screen.getByText("/work")).toBeInTheDocument();
    expect(screen.getByText("src")).toBeInTheDocument();
    expect(screen.queryByText("report.pdf")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("src"));
    expect(await screen.findByText("app.ts")).toBeInTheDocument();
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
    expect(await screen.findByText("app.ts")).toBeInTheDocument();
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

  it("searches workspace files without including attachments", async () => {
    renderList(newClient());
    await screen.findByText("Acme");
    // The files tab is source only, so the attachment is excluded from the tree;
    // filtering the source file keeps it and its ancestor folder.
    fireEvent.change(screen.getByLabelText(enChat.doc_file_list.filter_placeholder), {
      target: { value: "app.ts" },
    });
    expect(await screen.findByText("app.ts")).toBeInTheDocument();
    expect(screen.queryByText("report.pdf")).not.toBeInTheDocument();
  });

  it("shows a no-match state when the filter excludes everything", async () => {
    renderList(newClient());
    // Wait for the source (files) tab to load before filtering.
    await screen.findByText("Acme");
    fireEvent.change(screen.getByLabelText(enChat.doc_file_list.filter_placeholder), {
      target: { value: "zzz-nothing" },
    });
    expect(await screen.findByText(enChat.doc_file_list.no_match)).toBeInTheDocument();
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

  it("keeps an unavailable directory selected with retry instead of masking its error with repository links", async () => {
    session.current.workspaceError = new Error("directory has no matching runtime task binding");
    session.current.files = {
      ...session.current.files,
      repos: [{ url: "https://github.com/acme/widgets.git", description: "", ref: "main" } as never],
    };
    renderList(newClient());

    await screen.findByText("Acme");
    expect(await screen.findByRole("alert")).toHaveTextContent(enChat.doc_file_list.load_failed);
    expect(screen.queryByText("acme/widgets")).not.toBeInTheDocument();
    session.current.workspaceError = null;
    fireEvent.click(screen.getByRole("button", { name: enChat.doc_file_list.retry }));
    expect(await screen.findByText("src")).toBeInTheDocument();
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
    expect(await screen.findByText("app.ts")).toBeInTheDocument();
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

  it("matches the file tree to the task that owns the open document", async () => {
    session.current.files = {
      ...session.current.files,
      tasks: [
        { id: "lead", runtime_id: "rt-1", work_dir: "/lead", created_at: "2026-01-01" } as never,
        { id: "sibling", runtime_id: "rt-1", work_dir: "/sibling", created_at: "2026-01-02" } as never,
      ],
    };
    session.current.entries = [];
    session.current.entriesByPath = {
      "/lead": [{ path: "src/lead.ts", size: 10, directory: false }],
      "/sibling": [{ path: "src/sibling.ts", size: 20, directory: false }],
    };
    useDocModeStore.getState().openDoc("session-1", {
      kind: "workspace_file",
      id: "sibling:src/sibling.ts",
      label: "src/sibling.ts",
      sessionId: "session-1",
      taskId: "sibling",
      runtimeId: "rt-1",
      workDir: "/sibling",
      relativePath: "src/sibling.ts",
    });
    renderList(newClient());

    fireEvent.click(await screen.findByText("src"));
    expect(await screen.findByText("sibling.ts")).toBeInTheDocument();
    expect(screen.queryByText("lead.ts")).not.toBeInTheDocument();
    expect(screen.getByText("/sibling")).toBeInTheDocument();
  });

  it("lets the user switch from an unavailable worktree to another project directory", async () => {
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

    expect(await screen.findByRole("alert")).toHaveTextContent(enChat.doc_file_list.load_failed);
    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox", { name: "Directory" }));
    await user.click(await screen.findByRole("option", { name: /\/live$/ }));
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
