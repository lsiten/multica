import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import { requestReviewPage } from "../../platform/local-review-pages";

// A directory's pages contain only immediate children. Never recursively read
// unopened siblings, whose contents can be arbitrarily large.
export async function readWorkspaceDirectory(input: PagedReviewInput, relativePath: string, signal: AbortSignal) {
  const request = { ...input, action: "workspace_files", recursive: false, relative_path: relativePath, limit: 500 } as const;
  const first = await requestReviewPage({ ...request, offset: 0 }, signal);
  if (!("entries" in first)) throw new TypeError("Workspace file list response expected");
  if (first.truncated) throw new Error("Update the server and daemon to browse this directory completely");
  const entries = [...first.entries];
  let page = first;
  while (page.has_more) {
    const next = await requestReviewPage({ ...request, offset: page.next_offset }, signal);
    if (!("entries" in next) || next.next_offset === undefined || (next.has_more && next.next_offset <= (page.next_offset ?? 0))) throw new Error("Directory page did not advance");
    entries.push(...next.entries);
    page = next;
  }
  return { ...first, entries };
}
