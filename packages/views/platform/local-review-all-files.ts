import type { LocalReviewRequest } from "@multica/core/types/local-review";
import type { ReviewManifest } from "@multica/core/types/local-review-pages";
import { readReviewManifest } from "./local-review-pages";

export async function readAllReviewPaths(input: { readonly request: LocalReviewRequest; readonly manifest: ReviewManifest }, signal?: AbortSignal): Promise<string[]> {
  const paths = new Set<string>();
  let page = input.manifest;
  for (let batch = 0; batch < 100; batch++) {
    signal?.throwIfAborted();
    for (const file of page.page.files) {
      if (paths.has(file.path) || paths.size >= 10000) throw new Error("Invalid complete review file list");
      paths.add(file.path);
    }
    if (!page.page.has_more) {
      if (paths.size !== input.manifest.page.total_files) throw new Error("Incomplete review file list");
      return [...paths];
    }
    const offset = page.page.next_offset;
    page = await readReviewManifest({ ...input.request, target: input.manifest.header.target, action: "files", version_id: input.manifest.version_id, offset, limit: 200 }, signal);
    if (page.page.next_offset <= offset) throw new Error("Review file pagination did not advance");
  }
  throw new Error("Review file pagination limit exceeded");
}
