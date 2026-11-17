import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import type { DocumentTextLocation } from "@multica/core/chat";
import { isInternalWorkspacePath } from "@multica/core/types/local-review";
import { TEXT_PREVIEW_BASENAMES, TEXT_PREVIEW_EXTENSIONS } from "../../editor/utils/text-preview-whitelist.generated";
import { requestReviewPage } from "../../platform/local-review-pages";
import { readWorkspaceDirectory, type DirectorySearchCursor } from "./workspace-directory-query";

interface SearchFile { readonly path: string; readonly file_id?: string; }
export interface ContentSearchCursor {
  readonly directories?: DirectorySearchCursor;
  readonly files: readonly SearchFile[];
}
export interface ContentSearchMatch {
  readonly path: string;
  readonly fileId?: string;
  readonly directoryId?: string;
  readonly line: number;
  readonly text: string;
  readonly location: DocumentTextLocation;
}

function supportsContentSearch(path: string) {
  const basename = path.split("/").at(-1)?.toLowerCase() ?? "";
  const extension = basename.includes(".") ? basename.split(".").at(-1) ?? "" : "";
  return TEXT_PREVIEW_BASENAMES.has(basename) || TEXT_PREVIEW_EXTENSIONS.has(extension);
}

// Each explicit batch bounds directory work and file reads independently, so a
// tree of empty folders cannot monopolize traversal without yielding results.
export async function readWorkspaceContentSearchBatch(input: {
  readonly request: PagedReviewInput;
  readonly term: string;
  readonly cursor: ContentSearchCursor;
}, signal: AbortSignal) {
  let directories = input.cursor.directories;
  let files = input.cursor.files;
  let directoryReads = 0;
  let fileReads = 0;
  let scannedFiles = 0;
  const matches: ContentSearchMatch[] = [];
  const failedFiles: string[] = [];
  const truncatedFiles: string[] = [];
  const pattern = new RegExp(input.term.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "iu");
  while (fileReads < 8) {
    signal.throwIfAborted();
    const file = files[0];
    if (file) {
      files = files.slice(1);
      fileReads++;
      try {
        const response = await requestReviewPage({ ...input.request, action: "file_content", relative_path: file.path, file_id: file.file_id, offset: 0, limit: 65536 }, signal);
        signal.throwIfAborted();
        if (!("content" in response) || !("encoding" in response.content) || response.content.encoding !== "utf8") throw new TypeError("Text file content expected");
        scannedFiles++;
        if (response.content.has_more) truncatedFiles.push(file.path);
        let fileMatches = 0;
        let lineOffset = 0;
        for (const [index, line] of response.content.text.split("\n").entries()) {
          const match = pattern.exec(line);
          const startOffset = lineOffset;
          lineOffset += line.length + 1;
          if (!match) continue;
          const start = Math.max(0, match.index - 60);
          matches.push({ path: "path" in response ? response.path : file.path,
            fileId: "file_id" in response && typeof response.file_id === "string" ? response.file_id : undefined,
            directoryId: "directory_id" in response && typeof response.directory_id === "string" ? response.directory_id : undefined,
            line: index + 1, text: line.slice(start, start + 180), location: { offset: startOffset + match.index, text: match[0], line: index + 1 } });
          if (++fileMatches === 3) break;
        }
      } catch (error) {
        signal.throwIfAborted();
        if (!(error instanceof Error)) throw error;
        failedFiles.push(file.path);
      }
      continue;
    }
    if (!directories || directoryReads >= 4) break;
    const cursor = directories;
    const page = await readWorkspaceDirectory(input.request, cursor.path, signal, cursor.offset);
    directoryReads++;
    directories = cursor.next;
    if (page.has_more) directories = { path: cursor.path, offset: page.next_offset ?? 0, next: directories };
    for (const entry of page.entries) {
      if (isInternalWorkspacePath(entry.path)) continue;
      if (entry.directory) {
        if (page.recursive === false) directories = { path: entry.path, offset: 0, next: directories };
      } else if (supportsContentSearch(entry.path)) files = [...files, entry];
    }
  }
  const next: ContentSearchCursor | undefined = directories || files.length ? { directories, files } : undefined;
  return { matches, scannedFiles, failedFiles, truncatedFiles, next };
}
