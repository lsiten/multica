// @vitest-environment node
import { InfiniteQueryObserver, QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ListIssuesParams } from "@multica/core/types";
import { issueListOptions, loadedIssues } from "./issues";
import { refreshIssuePages } from "../realtime/issue-pages-refresh";

const mocks = vi.hoisted(() => ({ listIssues: vi.fn() }));
vi.mock("@/data/api", () => ({ api: { listIssues: mocks.listIssues } }));

const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
afterEach(() => { client.clear(); vi.resetAllMocks(); });

describe("mobile issue pagination", () => {
  it("loads every server page instead of silently stopping at the first 100 issues", async () => {
    const issues = Array.from({ length: 251 }, (_, index) => ({ id: `issue-${index}` }));
    mocks.listIssues.mockImplementation(async (params: ListIssuesParams) => ({
      issues: issues.slice(params.offset ?? 0, (params.offset ?? 0) + (params.limit ?? 100)),
      total: issues.length,
    }));
    const observer = new InfiniteQueryObserver(client, issueListOptions("ws-1"));

    await observer.refetch();
    await observer.fetchNextPage();
    const result = await observer.fetchNextPage();

    expect(result.data?.pages.flatMap((page) => page.issues)).toHaveLength(251);
    expect(result.hasNextPage).toBe(false);
    expect(mocks.listIssues.mock.calls.map(([params]) => params.offset)).toEqual([0, 100, 200]);
    observer.destroy();
  });

  it("sends filters to the server so matches outside the initial window remain visible", async () => {
    mocks.listIssues.mockResolvedValue({ issues: [{ id: "later-match" }], total: 1 });
    const filter = { statuses: ["blocked"], priorities: ["urgent"], assignee_types: ["agent", "squad"] } satisfies ListIssuesParams;
    const observer = new InfiniteQueryObserver(client, issueListOptions("ws-1", filter));

    const result = await observer.refetch();

    expect(mocks.listIssues).toHaveBeenCalledWith(expect.objectContaining({ ...filter, offset: 0, limit: 100 }), { signal: expect.any(AbortSignal) });
    expect(result.data?.pages[0]?.issues).toEqual([{ id: "later-match" }]);
    expect(issueListOptions("ws-1", filter).queryKey).not.toEqual(issueListOptions("ws-1").queryKey);
    observer.destroy();
  });

  it("keeps loaded rows unique when an offset boundary moves before the next page", async () => {
    mocks.listIssues.mockResolvedValueOnce({ issues: [{ id: "first" }, { id: "boundary" }], total: 3 })
      .mockResolvedValueOnce({ issues: [{ id: "boundary" }, { id: "last" }], total: 3 });
    const observer = new InfiniteQueryObserver(client, issueListOptions("ws-1"));
    await observer.refetch();

    const result = await observer.fetchNextPage();

    expect(loadedIssues(result.data).map((issue) => issue.id)).toEqual(["first", "boundary", "last"]);
    observer.destroy();
  });

  it("replaces an initial request after a live change without refreshing another workspace", async () => {
    let finishOld: (() => void) | undefined;
    mocks.listIssues.mockImplementationOnce(() => new Promise((resolve) => {
      finishOld = () => resolve({ issues: [{ id: "stale" }], total: 1 });
    })).mockResolvedValue({ issues: [{ id: "current" }], total: 1 });
    const otherKey = issueListOptions("other-workspace").queryKey;
    client.setQueryData(otherKey, { pages: [{ issues: [], total: 0 }], pageParams: [0] });
    const observer = new InfiniteQueryObserver(client, issueListOptions("ws-1"));
    const unsubscribe = observer.subscribe(() => {});

    await refreshIssuePages(client, "ws-1");
    finishOld?.();

    expect(loadedIssues(observer.getCurrentResult().data).map((issue) => issue.id)).toEqual(["current"]);
    expect(client.getQueryState(otherKey)?.isInvalidated).toBe(false);
    unsubscribe();
    observer.destroy();
  });
});
