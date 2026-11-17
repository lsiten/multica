// @vitest-environment node
import { beforeEach, expect, it, vi } from "vitest";
import { readWorkspaceContentSearchBatch } from "./workspace-content-search";

const read = vi.hoisted(() => vi.fn());
vi.mock("../../platform/local-review-pages", () => ({ requestReviewPage: read }));
const request = { workspace_id: "ws", project_resource_id: "resource", runtime_id: "runtime", path: "/root" };
const cursor = { directories: { path: ".", offset: 0 }, files: [] };
beforeEach(() => { read.mockReset(); });

it("searches text independently of filenames and preserves the bounded cursor", async () => {
  read.mockImplementation((input: { action: string; relative_path: string }) => {
    if (input.action === "workspace_files") return Promise.resolve({ recursive: false, truncated: false, entries: [
      { path: ".local-review-cache", directory: true, size: 0 },
      { path: "photo.png", directory: false, size: 200 },
      ...Array.from({ length: 10 }, (_, i) => ({ path: `file-${i}.md`, directory: false, size: 12 })),
    ], next_offset: 12, has_more: false });
    return Promise.resolve({ content: { text: "intro\nNEEDLE here", encoding: "utf8", has_more: input.relative_path === "file-0.md" } });
  });
  const first = await readWorkspaceContentSearchBatch({ request, term: "needle", cursor }, new AbortController().signal);
  expect(first.scannedFiles).toBe(8);
  expect(first.matches[0]).toEqual({ path: "file-0.md", line: 2, text: "NEEDLE here", location: { offset: 6, line: 2, text: "NEEDLE" } });
  expect(first.truncatedFiles).toEqual(["file-0.md"]);
  expect(first.next?.files.map((file) => file.path)).toEqual(["file-8.md", "file-9.md"]);
  if (!first.next) throw new Error("Expected unscanned files");
  const second = await readWorkspaceContentSearchBatch({ request, term: "needle", cursor: first.next }, new AbortController().signal);
  expect(second.matches.map((match) => match.path)).toEqual(["file-8.md", "file-9.md"]);
  expect(second.next).toBeUndefined();
  expect(read.mock.calls.filter(([input]) => input.action === "file_content").map(([input]) => input.relative_path)).toEqual(Array.from({ length: 10 }, (_, i) => `file-${i}.md`));
});

it("keeps sibling matches when a file becomes unreadable and does not claim complete coverage", async () => {
  read.mockResolvedValueOnce({ recursive: false, entries: [
    { path: "gone.md", directory: false, size: 5 }, { path: "good.md", directory: false, size: 5 },
  ], next_offset: 2, has_more: false, truncated: false })
    .mockRejectedValueOnce(new Error("file removed"))
    .mockResolvedValueOnce({ content: { text: "needle", encoding: "utf8", has_more: false } });
  const result = await readWorkspaceContentSearchBatch({ request, term: "needle", cursor }, new AbortController().signal);
  expect(result.failedFiles).toEqual(["gone.md"]);
  expect(result.scannedFiles).toBe(1);
  expect(result.matches).toEqual([{ path: "good.md", line: 1, text: "needle", location: { offset: 0, line: 1, text: "needle" } }]);
  expect(result.next).toBeUndefined();
});

it("stops empty-directory traversal after one batch and follows the remaining directory on continuation", async () => {
  read.mockImplementation((input: { relative_path: string }) => Promise.resolve({ recursive: false, entries: [{ path: `${input.relative_path}/child`, directory: true, size: 0 }], next_offset: 1, has_more: false, truncated: false }));
  const result = await readWorkspaceContentSearchBatch({ request, term: "needle", cursor }, new AbortController().signal);
  expect(read).toHaveBeenCalledTimes(4);
  expect(result.next?.directories?.path).toBe("./child/child/child/child");
});

it("propagates cancellation instead of recording it as an unreadable file", async () => {
  const controller = new AbortController();
  read.mockResolvedValueOnce({ recursive: false, entries: [{ path: "a.md", directory: false, size: 2 }], next_offset: 1, has_more: false, truncated: false }).mockImplementationOnce(() => {
    controller.abort();
    return Promise.reject(new DOMException("Aborted", "AbortError"));
  });
  await expect(readWorkspaceContentSearchBatch({ request, term: "x", cursor }, controller.signal)).rejects.toThrow(/abort/i);
});

it("preserves source offsets after Unicode case folding and treats punctuation literally", async () => {
  read.mockResolvedValueOnce({ content: { text: "İ then a+b", encoding: "utf8", has_more: false } });
  const result = await readWorkspaceContentSearchBatch({ request, term: "a+b", cursor: { files: [{ path: "a.md" }] } }, new AbortController().signal);
  expect(result.matches[0]?.location).toEqual({ offset: 7, text: "a+b", line: 1 });
});
