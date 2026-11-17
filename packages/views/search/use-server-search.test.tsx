import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useServerSearch } from "./use-server-search";

const mocks = vi.hoisted(() => ({ issues: vi.fn(), projects: vi.fn() }));
vi.mock("@multica/core/search-index", () => ({
  isLocalSearchReady: () => false,
  searchIssues: mocks.issues,
  searchProjects: mocks.projects,
}));
beforeEach(() => {
  vi.useFakeTimers();
  mocks.issues.mockReset();
  mocks.projects.mockReset().mockResolvedValue({ projects: [] });
});
afterEach(() => vi.useRealTimers());

describe("useServerSearch failure recovery", () => {
  it("exposes failure separately from an empty result and retries the same query", async () => {
    const failure = new Error("unavailable");
    mocks.issues.mockRejectedValueOnce(failure).mockResolvedValue({ issues: [] });
    const { result } = renderHook(() => useServerSearch("missing invoice", { projects: true }));
    await act(() => vi.advanceTimersByTimeAsync(300));

    expect(result.current.error).toBe(failure);
    act(() => result.current.retry());
    await act(() => vi.advanceTimersByTimeAsync(300));

    expect(result.current.error).toBeNull();
    expect(result.current.results.query).toBe("missing invoice");
    expect(mocks.issues).toHaveBeenCalledTimes(2);
  });

  it("ignores a cancelled query failure after the user has typed another query", async () => {
    let rejectOld: ((error: Error) => void) | undefined;
    mocks.issues.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectOld = reject; }))
      .mockResolvedValue({ issues: [] });
    const { result, rerender } = renderHook((query) => useServerSearch(query, { projects: false }), { initialProps: "old" });
    await act(() => vi.advanceTimersByTimeAsync(300));

    rerender("current");
    await act(async () => { rejectOld?.(new Error("aborted")); await vi.advanceTimersByTimeAsync(300); });

    expect(result.current.error).toBeNull();
    expect(result.current.results.query).toBe("current");
  });
});
