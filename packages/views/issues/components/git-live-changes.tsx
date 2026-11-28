import { useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { readGitManifest, readLocalIndex, readGitHistory, readReviewManifest } from "../../platform/local-review-pages";
import { LocalReviewFileBrowser } from "./local-review-file-browser";
import { LocalReviewError } from "./local-review-error";
import type { GitChangesRequest } from "./git-changes-panel";

export function GitUncommittedChanges({ request }: { readonly request: GitChangesRequest }) {
  const { t } = useT("issues");
  const [side, setSide] = useState<"working" | "staged">("working");
  const index = useQuery({ queryKey: ["git-uncommitted", request], queryFn: ({ signal }) => readLocalIndex(request, signal), retry: false, networkMode: "always", refetchOnWindowFocus: false });
  const data = index.isError ? undefined : index.data;
  const versionId = side === "working" ? data?.unstaged_version_id : data?.staged_version_id;
  const diff = useQuery({
    queryKey: ["git-uncommitted-diff", request, side, versionId],
    queryFn: ({ signal }) => readReviewManifest({ ...request, action: "files", target: data?.status.branch, version_id: versionId }, signal),
    enabled: !!versionId, retry: false, networkMode: "always", refetchOnWindowFocus: false,
  });
  return <div className="space-y-3">
    <Button variant="outline" size="sm" disabled={index.isFetching} onClick={() => void index.refetch()}>{t(($) => $.local_review.refresh)}</Button>
    {index.isPending && <p role="status">{t(($) => $.local_review.loading)}</p>}
    {index.error && <LocalReviewError error={index.error} id="git-index-error" />}
    {data && <>
      <p className="break-all text-caption">{data.status.branch} · {data.status.head.slice(0, 12)}</p>
      {data.status.files.length === 0 ? <p role="status">{t(($) => $.local_review.empty)}</p> : <>
        <div className="flex gap-1"><Button size="sm" variant={side === "working" ? "secondary" : "ghost"} aria-pressed={side === "working"} onClick={() => setSide("working")}>{t(($) => $.local_review.unstaged)}</Button><Button size="sm" variant={side === "staged" ? "secondary" : "ghost"} aria-pressed={side === "staged"} onClick={() => setSide("staged")}>{t(($) => $.local_review.staged)}</Button></div>
        {diff.isFetching && <p role="status">{t(($) => $.local_review.loading)}</p>}
        {diff.error && <LocalReviewError error={diff.error} id="git-index-diff-error" />}
        {!diff.error && diff.data ? <LocalReviewFileBrowser key={diff.data.version_id} request={{ ...request, target: data.status.branch }} manifest={diff.data} /> : !diff.isFetching && !diff.error && <>
          <ul className="space-y-1 text-caption">{data.status.files.filter((file) => side === "staged" ? file.staged : file.unstaged || file.untracked).map((file) => <li key={file.path} className="break-all">{file.old_path ? `${file.old_path} → ` : ""}{file.path} · {file.index_code}{file.working_code}</li>)}</ul>
          <p className="text-caption text-muted-foreground">{t(($) => $.git_changes.diff_unavailable)}</p>
        </>}
      </>}
    </>}
  </div>;
}

export function GitCommitList({ request }: { readonly request: GitChangesRequest }) {
  const { t } = useT("issues");
  const [selectedCommit, setSelectedCommit] = useState("");
  const commits = useInfiniteQuery({
    queryKey: ["git-commits", request],
    queryFn: ({ pageParam, signal }) => readGitHistory({ ...request, offset: pageParam, limit: 100 }, signal),
    initialPageParam: 0,
    getNextPageParam: (last) => last.has_more ? last.next_offset : undefined,
    retry: false, networkMode: "always", refetchOnWindowFocus: false,
  });
  const commit = selectedCommit || commits.data?.pages[0]?.commits[0]?.sha || "";
  const diff = useQuery({
    queryKey: ["git-commit-diff", request, commit],
    queryFn: ({ signal }) => readGitManifest({ ...request, git_scope: "commit", commit_sha: commit, target: "" }, signal),
    enabled: !!commit, retry: false, networkMode: "always", refetchOnWindowFocus: false,
  });
  return <div className="space-y-2">
    {commits.isPending && <p role="status">{t(($) => $.local_review.loading)}</p>}
    {commits.error && <LocalReviewError error={commits.error} id="git-commits-error" />}
    {commits.data && <ul className="space-y-2">{commits.data.pages.flatMap((page) => page.commits).map((commit) => <li key={commit.sha} className="break-words text-caption"><Button variant={(selectedCommit || commits.data?.pages[0]?.commits[0]?.sha) === commit.sha ? "secondary" : "ghost"} className="h-auto min-h-8 max-w-full whitespace-normal text-left" aria-pressed={(selectedCommit || commits.data?.pages[0]?.commits[0]?.sha) === commit.sha} onClick={() => setSelectedCommit(commit.sha)}><code title={commit.sha}>{commit.sha.slice(0, 12)}</code> {commit.subject}</Button></li>)}</ul>}
    {commits.data?.pages[0]?.commits.length === 0 && <p role="status">{t(($) => $.local_review.empty)}</p>}
    {diff.isFetching && <p role="status">{t(($) => $.local_review.loading)}</p>}
    {diff.error && <LocalReviewError error={diff.error} id="git-commit-diff-error" />}
    {!diff.error && diff.data && <LocalReviewFileBrowser key={diff.data.version_id} request={request} manifest={diff.data} />}
    {commits.hasNextPage && <Button variant="outline" disabled={commits.isFetchingNextPage} onClick={() => void commits.fetchNextPage()}>{t(($) => $.local_review.load_more)}</Button>}
  </div>;
}
