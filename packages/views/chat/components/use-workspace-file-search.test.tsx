// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { useWorkspaceFileSearch } from "./use-workspace-file-search";
const read = vi.hoisted(() => vi.fn());
vi.mock("./workspace-directory-query", () => ({ readWorkspaceSearchPage: read }));
beforeEach(() => { read.mockReset(); });
const request = { workspace_id:"ws", project_resource_id:"resource", path:"/project", runtime_id:"runtime" };
function createWrapper() {
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  return function Wrapper({children}: {children: ReactNode}) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}
it("keeps partial matches on failure and resumes the failed page on retry", async () => {
  const next = {path:"docs",offset:100};
  read.mockResolvedValueOnce({entries:[{path:"first.md"}],next})
    .mockRejectedValueOnce(new Error("daemon disconnected"))
    .mockResolvedValueOnce({entries:[{path:"last.md"}],next:undefined});
  const {result} = renderHook(() => useWorkspaceFileSearch(request, ["files"], "md", true), {wrapper:createWrapper()});
  await waitFor(() => expect(result.current.isError).toBe(true));
  expect(result.current.entries.map((entry) => entry.path)).toEqual(["first.md"]);
  expect(read).toHaveBeenCalledTimes(2);
  await act(async () => {await result.current.fetchNextPage();});
  await waitFor(() => expect(result.current.searching).toBe(false));
  expect(result.current.entries.map((entry) => entry.path)).toEqual(["first.md", "last.md"]);
  expect(read.mock.calls[2]?.[2]).toEqual(next);
});
it("cancels the previous term and isolates the replacement search", async () => {
  let oldSignal: AbortSignal | undefined;
  read.mockImplementationOnce((_request, _term, _cursor, signal: AbortSignal) => {
    oldSignal = signal;
    return new Promise((_resolve, reject) => signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))));
  }).mockResolvedValueOnce({entries:[{path:"new.md"}],next:undefined});
  const {result, rerender} = renderHook(({term}) => useWorkspaceFileSearch(request, ["files"], term, true), {initialProps:{term:"old"}, wrapper:createWrapper()});
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  rerender({term:"new"});
  await waitFor(() => expect(result.current.entries.map((entry) => entry.path)).toEqual(["new.md"]));
  expect(oldSignal?.aborted).toBe(true);
});
it("cancels an in-flight scan when the files tab is hidden", async () => {
  let signal: AbortSignal | undefined;
  read.mockImplementation((_request, _term, _cursor, currentSignal: AbortSignal) => {
    signal=currentSignal;
    return new Promise((_resolve, reject) => currentSignal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))));
  });
  const {rerender} = renderHook(({enabled}) => useWorkspaceFileSearch(request, ["files"], "md", enabled), {initialProps:{enabled:true},wrapper:createWrapper()});
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  rerender({enabled:false});
  await waitFor(() => expect(signal?.aborted).toBe(true));
});
