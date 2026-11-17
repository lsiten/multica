// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { useWorkspaceContentSearch } from "./use-workspace-content-search";

const read = vi.hoisted(() => vi.fn());
vi.mock("./workspace-content-search", () => ({ readWorkspaceContentSearchBatch: read }));
const request = { workspace_id: "ws", project_resource_id: "resource", path: "/project", runtime_id: "runtime" };
function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return function Wrapper({ children }: { readonly children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}
beforeEach(() => { read.mockReset(); });

it("waits for explicit continuation and retains prior matches and incomplete coverage", async () => {
  read.mockResolvedValueOnce({ matches: [{ path: "first.md" }], scannedFiles: 8, failedFiles: ["gone.md"], truncatedFiles: ["large.md"], next: { files: [{ path: "last.md" }] } })
    .mockResolvedValueOnce({ matches: [{ path: "last.md" }], scannedFiles: 1, failedFiles: [], truncatedFiles: [], next: undefined });
  const { result } = renderHook(() => useWorkspaceContentSearch({ request, directoryKey: ["root"], term: "needle", enabled: true }), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.hasNextPage).toBe(true));
  expect(read).toHaveBeenCalledTimes(1);
  expect(result.current.scannedFiles).toBe(8);
  await act(async () => { await result.current.fetchNextPage(); });
  await waitFor(() => expect(result.current.matches.map((match) => match.path)).toEqual(["first.md", "last.md"]));
  expect(result.current.scannedFiles).toBe(9);
  expect(result.current.failedFiles).toEqual(["gone.md"]);
  expect(result.current.truncatedFiles).toEqual(["large.md"]);
  expect(result.current.hasNextPage).toBe(false);
});

it("cancels a hidden scan and isolates another directory's results", async () => {
  let signal: AbortSignal | undefined;
  read.mockImplementationOnce((_input, activeSignal: AbortSignal) => {
    signal = activeSignal;
    return new Promise((_resolve, reject) => activeSignal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))));
  }).mockResolvedValueOnce({ matches: [{ path: "other.md" }], scannedFiles: 1, failedFiles: [], truncatedFiles: [], next: undefined });
  const { result, rerender } = renderHook(({ root, enabled }) => useWorkspaceContentSearch({ request, directoryKey: [root], term: "needle", enabled }), { initialProps: { root: "first", enabled: true }, wrapper: wrapper() });
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  rerender({ root: "first", enabled: false });
  await waitFor(() => expect(signal?.aborted).toBe(true));
  rerender({ root: "other", enabled: true });
  await waitFor(() => expect(result.current.matches.map((match) => match.path)).toEqual(["other.md"]));
});
