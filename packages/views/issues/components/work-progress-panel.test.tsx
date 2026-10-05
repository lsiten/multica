import { useState, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError, api } from "@multica/core/api";
import { IssueProgressViewSchema } from "@multica/core/api/issue-progress";
import type { IssueProgressView } from "@multica/core/types/issue-progress";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import { WorkProgressPanel } from "./work-progress-panel";
import { IssueWorkProgress } from "./issue-work-progress";

const navigationCalls = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }));
vi.mock("@multica/core/api", async () => ({ ...await vi.importActual<typeof import("@multica/core/api")>("@multica/core/api"), api: { getWorkProgress: vi.fn() } }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws" }));
vi.mock("@multica/core/auth", () => ({ useAuthStore: Object.assign((selector: (state: { user: { id: string } }) => unknown) => selector({ user: { id: "me" } }), { getState: () => ({ user: { id: "me" } }) }) }));
vi.mock("@multica/core/workspace/hooks", () => ({ useActorName: () => ({ getActorName: (_type: string, id: string) => id }) }));
vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: async () => [] }),
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: async () => [] }),
  squadListOptions: () => ({ queryKey: ["squads"], queryFn: async () => [] }),
}));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({ issueDetail: (id: string) => `/ws/issues/${id}` }) }));
vi.mock("../utils/status-label", () => ({ useStatusLabel: () => (status: string) => status }));
vi.mock("../../common/human-request-card", () => ({ HumanRequestCard: ({ requestId }: { requestId: string }) => <div data-human-request-id={requestId}>Request: {requestId}</div> }));
vi.mock("../../common/task-transcript/agent-transcript-dialog", () => ({ AgentTranscriptDialog: () => null }));

function view(type: "issue" | "project" = "project"): IssueProgressView {
  return IssueProgressViewSchema.parse({
    workspace_id: "ws", scope: { type, id: "root" }, as_of: "2026-10-05T00:00:00Z", version: "v1", complete: true,
    summary: { total: 2, done: 0, closed: 0, open: 2, open_leaf: 1, open_parent: 1, attention: 1, pending_decisions: 0 },
    filtered_total: 1,
    items: [{ issue: { id: "leaf", identifier: "MUL-3", title: "Deep callback fix", status: "todo" }, attention: true, reasons: ["dependency"],
      path: [{ id: "root", identifier: "MUL-1", title: "Payment goal", status: "in_progress" }, { id: "parent", identifier: "MUL-2", title: "Integration", status: "in_progress" }],
      group: { id: "parent", identifier: "MUL-2", title: "Integration", status: "in_progress" },
      direct_blockers: [{ id: "integration-blocker", identifier: "MUL-5", title: "Integration prerequisite", status: "todo" }],
      root_blockers: [{ id: "blocker", identifier: "MUL-4", title: "Repair API", status: "todo" }],
    }],
  });
}

function renderProgress(children: ReactNode, path = "/ws/projects/root", deferRouteUpdates = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  function Providers() {
    const [location, setLocation] = useState(path);
    const url = new URL(location, "https://app.test");
    const adapter: NavigationAdapter = {
      pathname: url.pathname, searchParams: url.searchParams, hash: url.hash,
      push: navigationCalls.push, replace: next => { navigationCalls.replace(next); if (!deferRouteUpdates) setLocation(next); }, back: vi.fn(), getShareableUrl: path => `https://app.test${path}`,
    };
    return <QueryClientProvider client={client}><NavigationProvider value={adapter}>{children}</NavigationProvider></QueryClientProvider>;
  }
  const rendered = renderWithI18n(<Providers />);
  return { ...rendered, client };
}

beforeEach(() => { vi.clearAllMocks(); vi.mocked(api.getWorkProgress).mockResolvedValue(view()); });
afterEach(cleanup);

describe("work progress surfaces", () => {
  it("retains direct child actions by default and exposes deep work on demand", async () => {
    vi.mocked(api.getWorkProgress).mockResolvedValue(view("issue"));
    renderProgress(<IssueWorkProgress issueId="root" projectId={null} hasChildren><button>Edit direct child</button></IssueWorkProgress>, "/ws/issues/root");
    expect(screen.getByRole("button", { name: "Edit direct child" })).toBeVisible();
    await screen.findByText("Unfinished: 2");
    expect(screen.queryByText(/Deep callback fix/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Remaining work" }));
    await screen.findByText(/Deep callback fix/);
    expect(screen.getByRole("button", { name: "Remaining work" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.queryByRole("button", { name: "Edit direct child" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Direct sub-issues" }));
    expect(screen.getByRole("button", { name: "Edit direct child" })).toBeVisible();
  });

  it("shows recorded blockers, full ancestry and navigation without changing work", async () => {
    renderProgress(<WorkProgressPanel scope={{ type: "project", id: "root" }} />);
    await screen.findByText(/Deep callback fix/);
    expect(screen.getByText("Final prerequisites")).toBeVisible();
    const blockerLinks = screen.getAllByRole("link", { name: "MUL-4 · Repair API" });
    expect(blockerLinks).toHaveLength(1);
    await userEvent.click(blockerLinks[0]!);
    expect(navigationCalls.push).toHaveBeenCalledWith("/ws/issues/MUL-4");
    expect(api.getWorkProgress).toHaveBeenCalledWith({ type: "project", id: "root" }, expect.anything(), undefined, expect.any(AbortSignal));
  });

  it("keeps filters in the route and preserves unrelated parameters", async () => {
    renderProgress(<WorkProgressPanel scope={{ type: "project", id: "root" }} />, "/ws/projects/root?project_tab=attention&existing=value");
    await screen.findByText(/Deep callback fix/);
    await userEvent.click(screen.getByRole("button", { name: "Blocked" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Needs my action" }));
    await waitFor(() => expect(api.getWorkProgress).toHaveBeenLastCalledWith({ type: "project", id: "root" }, expect.objectContaining({ filter: "blocked", mine: true }), undefined, expect.any(AbortSignal)));
    expect(navigationCalls.replace).toHaveBeenLastCalledWith(expect.stringContaining("existing=value"));
    expect(screen.getByRole("button", { name: "Blocked" })).toHaveAttribute("aria-pressed", "true");
  });

  it("keeps original controls usable when progress cannot be read", async () => {
    vi.mocked(api.getWorkProgress).mockRejectedValue(new ApiError("not supported", 404, "Not Found"));
    renderProgress(<IssueWorkProgress issueId="root" projectId={null} hasChildren><button>Edit direct child</button></IssueWorkProgress>, "/ws/issues/root");
    await screen.findByRole("alert");
    expect(screen.getByRole("button", { name: "Edit direct child" })).toBeVisible();
    expect(screen.queryByText(/No unfinished descendants/)).not.toBeInTheDocument();
  });

  it("updates the decision checkbox immediately while route persistence is pending", async () => {
    renderProgress(<WorkProgressPanel scope={{ type: "project", id: "root" }} />, "/ws/projects/root", true);
    const checkbox = await screen.findByRole("checkbox", { name: "Needs my action" });
    await userEvent.click(checkbox);
    expect(checkbox).toBeChecked();
    expect(navigationCalls.replace).toHaveBeenCalledWith(expect.stringContaining("work_mine=true"));
  });

  it("renders unknown reasons safely and marks incomplete counts", async () => {
    const response = view(); response.complete = false; response.items[0]!.reasons = ["future_reason"];
    vi.mocked(api.getWorkProgress).mockResolvedValue(response);
    renderProgress(<WorkProgressPanel scope={{ type: "project", id: "root" }} />);
    await screen.findByText("Needs follow-up");
    expect(screen.getByText("Partial counts")).toBeVisible();
    expect(screen.queryByText("No issues currently require intervention.")).not.toBeInTheDocument();
  });

  it("does not hide incomplete relationships on a leaf issue", async () => {
    const response = view("issue");
    response.complete = false;
    response.summary = { total: 0, done: 0, closed: 0, open: 0, open_leaf: 0, open_parent: 0, attention: 0, pending_decisions: 0 };
    response.items = [];
    response.filtered_total = 0;
    vi.mocked(api.getWorkProgress).mockResolvedValue(response);
    renderProgress(<IssueWorkProgress issueId="root" projectId={null} hasChildren={false}><button>Edit issue</button></IssueWorkProgress>, "/ws/issues/root");
    await screen.findByText("Some relationships or records could not be fully evaluated. These counts do not prove completion.");
    expect(screen.getByRole("button", { name: "Edit issue" })).toBeVisible();
  });

  it("links a parent request to its original comment instead of duplicating the card", async () => {
    const response = view("issue");
    response.root = { ...response.items[0]!, issue: { ...response.items[0]!.issue, id: "root", identifier: "MUL-1" }, reasons: ["pending_me"], requests: [{ id: "request", recipient_id: "me", needs_me: true, expires_at: "2026-10-12T00:00:00Z" }] };
    vi.mocked(api.getWorkProgress).mockResolvedValue(response);
    const { container } = renderProgress(<IssueWorkProgress issueId="root" projectId={null} hasChildren><div data-human-request-id="request">Original card</div></IssueWorkProgress>, "/ws/issues/root");
    const link = await screen.findByRole("link", { name: "Respond to request" });
    expect(link).toHaveAttribute("href", "/ws/issues/MUL-1#comment-request");
    expect(container.querySelectorAll('[data-human-request-id="request"]')).toHaveLength(1);
  });

  it("restarts pagination when the server reports a changed snapshot", async () => {
    const old = view(); old.has_more = true; old.next_cursor = "old-cursor"; old.filtered_total = 2;
    const fresh = view(); fresh.version = "v2"; fresh.items[0]!.issue = { ...fresh.items[0]!.issue, id: "fresh", title: "Updated remaining work" };
    let changed = false;
    vi.mocked(api.getWorkProgress).mockImplementation(async (_scope, _filters, cursor) => {
      if (cursor) { changed = true; throw new ApiError("snapshot changed", 409, "Conflict", { code: "progress_snapshot_changed" }); }
      return changed ? fresh : old;
    });
    renderProgress(<WorkProgressPanel scope={{ type: "project", id: "root" }} />);
    await screen.findByText(/Deep callback fix/);
    await userEvent.click(screen.getByRole("button", { name: "Load more" }));
    await screen.findByText(/Updated remaining work/);
    expect(screen.queryByText(/Deep callback fix/)).not.toBeInTheDocument();
  });
});
