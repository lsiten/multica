import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import { isInternalWorkspacePath } from "@multica/core/types/local-review";
import { requestReviewPage } from "../../platform/local-review-pages";

// A read is one page of immediate children, independent of directory size.
export async function readWorkspaceDirectory(input: PagedReviewInput, relativePath: string, signal: AbortSignal, offset = 0) {
  const page = await requestReviewPage({ ...input, action: "workspace_files", recursive: false, relative_path: relativePath, limit: 100, offset }, signal);
  if (!("entries" in page)) throw new TypeError("Workspace file list response expected");
  if (page.truncated) throw new Error("Update the server and daemon to browse this directory completely");
  if (page.has_more && (page.next_offset === undefined || page.next_offset <= offset)) throw new Error("Directory page did not advance");
  return page;
}

export interface DirectorySearchCursor {
  path: string;
  offset: number;
  next?: DirectorySearchCursor;
}

// A persistent work list shares its tail between pages instead of copying a
// potentially large queue into every cached page. Each step reads just one page.
export async function readWorkspaceSearchPage(input: PagedReviewInput, term: string, cursor: DirectorySearchCursor, signal: AbortSignal) {
  const page = await readWorkspaceDirectory(input, cursor.path, signal, cursor.offset);
  let next = cursor.next;
  if (page.has_more) next = { path: cursor.path, offset: page.next_offset ?? 0, next };
  if (page.recursive === false) {
    for (const entry of page.entries) {
      if (entry.directory && !isInternalWorkspacePath(entry.path)) next = { path: entry.path, offset: 0, next };
    }
  }
  return { entries: page.entries.filter((entry) => !isInternalWorkspacePath(entry.path) && entry.path.toLowerCase().includes(term)), next };
}
