"use client";

import { useId, useState } from "react";
import { skipToken, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import type { ReviewManifest } from "@multica/core/types/local-review-pages";
import { defaultReviewTarget } from "@multica/core/types/local-review-target";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { readLocalReviewBranches } from "../../platform/local-review";
import { readReviewCommits, readReviewManifest, readReviewRepositories, renewReviewLease } from "../../platform/local-review-pages";
import { useT } from "../../i18n";
import { LocalReviewError, reviewErrorKind } from "./local-review-error";
import { LocalReviewTargetPicker } from "./local-review-target-picker";
import { LocalReviewFileBrowser } from "./local-review-file-browser";

type Decision = "submit" | "approve" | "request_changes" | "merge";

export function LocalReviewDialog({ request, onClose }: { request: LocalReviewRequest; onClose: () => void }) {
  const { t } = useT("issues");
  const errorId = useId();
  const client = useQueryClient();
  const userId = useAuthStore((state) => state.user?.id);
  const runtimeKey = ["local-review-runtime", userId, request.workspace_id, request.task_id];
  const runtime = useQuery<string>({ queryKey: runtimeKey, queryFn: skipToken, initialData: request.runtime_id || undefined });
  const [chosenRepository, setRepository] = useState<string | null>(null);
  const [chosenTarget, setTarget] = useState<string | null>(null);
  const [draft, setDraft] = useState<string | null>(null);
  const [comment, setComment] = useState("");
  const [confirmMerge, setConfirmMerge] = useState(false);
  const repositories = useQuery({
    queryKey: ["review-repositories", userId, request.workspace_id, request.task_id, request.path],
    queryFn: async ({ signal }) => {
      const result = await readReviewRepositories({ ...request, action: "repositories", runtime_id: request.runtime_id || runtime.data }, signal);
      if (result.runtime_id) client.setQueryData(runtimeKey, result.runtime_id);
      return result;
    },
    retry: false, staleTime: 30000, refetchOnWindowFocus: false, networkMode: "always",
  });
  const repositoryPath = chosenRepository ?? (repositories.data?.repositories.includes(request.path) ? request.path : repositories.data?.repositories[0]) ?? request.path;
  const scope = { ...request, runtime_id: request.runtime_id || runtime.data, path: repositoryPath };
  const branches = useQuery({
    queryKey: ["local-review-branches", userId, request.workspace_id, request.task_id, repositoryPath],
    queryFn: ({ signal }) => readLocalReviewBranches(scope, signal), enabled: !!repositories.data?.repositories.length,
    retry: false, staleTime: 30000, refetchOnWindowFocus: false, networkMode: "always",
  });
  const target = chosenTarget ?? defaultReviewTarget(branches.data ?? []);
  const targetDraft = draft ?? target;
  const key = ["paged-local-review", userId, request.workspace_id, request.task_id, repositoryPath, target];
  const manifest = useQuery({
    queryKey: key, enabled: !!target,
    queryFn: async ({ signal }) => {
      const result = await readReviewManifest({ ...scope, target, action: "manifest" }, signal);
      if (result.runtime_id) client.setQueryData(runtimeKey, result.runtime_id);
      return result;
    },
    retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false, networkMode: "always",
  });
  const operation = useMutation({
    networkMode: "always",
    mutationFn: (action: Decision) => {
      if (!manifest.data) throw new Error("Load the review version first");
      return readReviewManifest({ ...scope, runtime_id: scope.runtime_id || manifest.data.runtime_id, target, action, version_id: manifest.data.version_id, snapshot_id: manifest.data.version_id, comment });
    },
    onSuccess: (result) => { client.setQueryData(key, result); setConfirmMerge(false); },
  });
  const data = manifest.data;
  const lease = useQuery({
    queryKey: ["review-lease", userId, request.workspace_id, request.task_id, data?.version_id],
    queryFn: ({ signal }) => {
      if (!data) throw new Error("Review version unavailable");
      return renewReviewLease({ ...scope, action: "lease", runtime_id: scope.runtime_id || data.runtime_id, target: data.header.target, version_id: data.version_id }, signal);
    },
    enabled: !!data, refetchInterval: 60000, refetchIntervalInBackground: true, retry: false, networkMode: "always",
  });
  const pending = repositories.isFetching || branches.isFetching || manifest.isFetching || operation.isPending;
  const targetChanged = targetDraft !== target;
  const busy = pending || targetChanged;
  const error = operation.error ?? manifest.error ?? branches.error ?? repositories.error ?? lease.error;
  const merged = data?.review.state === "merged";
  const effectiveScope = { ...scope, runtime_id: scope.runtime_id || data?.runtime_id, target };
  return <Dialog open onOpenChange={(open) => { if (!open && !operation.isPending) onClose(); }}>
    <DialogContent className="flex h-[85vh] w-[95vw] max-w-6xl flex-col sm:max-w-6xl">
      <DialogHeader><DialogTitle>{t(($) => $.local_review.title)}</DialogTitle><DialogDescription className="break-all">{repositoryPath}</DialogDescription></DialogHeader>
      {repositories.data && repositories.data.repositories.length > 1 && <select aria-label={t(($) => $.local_review.repository)} value={repositoryPath} disabled={operation.isPending} className="rounded border bg-background p-2 text-caption" onChange={(event) => { setRepository(event.target.value); setTarget(null); setDraft(null); setConfirmMerge(false); operation.reset(); }}>{repositories.data.repositories.map((path) => <option key={path} value={path}>{path}</option>)}</select>}
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-caption">{data?.header.branch ?? "—"} →</span>
        <LocalReviewTargetPicker branches={branches.data ?? []} value={targetDraft} disabled={!branches.data?.length || operation.isPending} invalid={!targetChanged && !!error && reviewErrorKind(error) === "target"} descriptionId={error ? errorId : undefined} onChange={(value) => { setDraft(value); setConfirmMerge(false); operation.reset(); }} />
        <Button variant="outline" disabled={pending || !targetDraft} onClick={() => { setConfirmMerge(false); operation.reset(); if (targetChanged) setTarget(targetDraft); else void manifest.refetch(); }}>{targetChanged ? t(($) => $.local_review.apply_target) : t(($) => $.local_review.refresh)}</Button>
        {data && <span className="text-caption">{data.review.state === "open" ? t(($) => $.local_review.state_open) : t(($) => $.local_review[data.review.state])} · {data.page.total_files} {t(($) => $.local_review.files)} · {t(($) => $.local_review.file_changes, { additions: data.page.additions, deletions: data.page.deletions })}</span>}
      </div>
      {error && <LocalReviewError error={error} id={errorId} />}
      {pending && <p role="status" className="text-caption">{t(($) => $.local_review.loading)}</p>}
      {repositories.isSuccess && !repositories.data.repositories.length && <p role="status" className="text-caption">{t(($) => $.local_review.no_worktrees)}</p>}
      {branches.isSuccess && !branches.data.length && <p role="status" className="text-caption">{t(($) => $.local_review.no_local_branches)}</p>}
      {data && <>
        {data.header.dirty && <p className="text-caption text-warning">{t(($) => $.local_review.dirty)}</p>}
        <LocalReviewFileBrowser key={data.version_id} request={effectiveScope} manifest={data} />
        <LocalReviewCommits key={"commits-" + data.version_id} request={effectiveScope} manifest={data} />
        {!!data.review.events.length && <details className="text-caption"><summary>{t(($) => $.local_review.history)}</summary><ol className="max-h-32 space-y-2 overflow-auto py-2">{data.review.events.map((event, index) => <li key={index} className="rounded border p-2"><p>{event.actor_name || event.actor_id || t(($) => $.local_review.former_member)} · {event.kind === "approve" ? t(($) => $.local_review.approved) : event.kind === "request_changes" ? t(($) => $.local_review.changes_requested) : event.kind === "merge" || event.kind === "merge_recovered" ? t(($) => $.local_review.merged) : t(($) => $.local_review.submit)} · <time dateTime={event.created_at}>{new Date(event.created_at).toLocaleString()}</time></p>{event.comment && <p className="mt-1 whitespace-pre-wrap break-words">{event.comment}</p>}</li>)}</ol></details>}
        <Input value={comment} onChange={(event) => setComment(event.target.value)} placeholder={t(($) => $.local_review.comment)} aria-label={t(($) => $.local_review.comment)} maxLength={8000} />
        <div className="flex flex-wrap justify-end gap-2">
          <Button variant="outline" disabled={busy || merged} onClick={() => operation.mutate("submit")}>{t(($) => $.local_review.submit)}</Button>
          <Button variant="outline" disabled={busy || merged} onClick={() => operation.mutate("request_changes")}>{t(($) => $.local_review.request_changes)}</Button>
          <Button variant="outline" disabled={busy || merged} onClick={() => operation.mutate("approve")}>{t(($) => $.local_review.approve)}</Button>
          <Button disabled={busy || merged || data.header.dirty || data.header.branch === target || data.review.state !== "approved"} onClick={() => setConfirmMerge(true)}>{t(($) => $.local_review.merge)}</Button>
        </div>
        {confirmMerge && <div role="alert" className="flex flex-wrap items-center justify-between gap-2 rounded border p-3 text-caption">
          <span>{t(($) => $.local_review.branch_comparison, { branch: data.header.branch, head: data.header.head.slice(0, 8), target, targetHead: data.header.target_head.slice(0, 8) })}</span>
          <Button disabled={busy} onClick={() => operation.mutate("merge")}>{t(($) => $.local_review.confirm_merge)}</Button>
          <Button variant="ghost" onClick={() => setConfirmMerge(false)}>{t(($) => $.local_review.cancel)}</Button>
        </div>}
        {merged && <p role="status" className="text-caption text-success">{t(($) => $.local_review.merged)} {data.review.merged_commit}</p>}
      </>}
    </DialogContent>
  </Dialog>;
}

function LocalReviewCommits({ request, manifest }: { request: LocalReviewRequest; manifest: ReviewManifest }) {
  const { t } = useT("issues");
  const errorId = useId();
  const [open, setOpen] = useState(false);
  const commits = useInfiniteQuery({
    queryKey: ["review-version-commits", request.workspace_id, request.task_id, manifest.version_id],
    queryFn: ({ pageParam, signal }) => readReviewCommits({ ...request, action: "commits", version_id: manifest.version_id, offset: pageParam, limit: 50 }, signal),
    initialPageParam: 0, getNextPageParam: (last) => last.has_more ? last.next_offset : undefined,
    enabled: open, retry: false, staleTime: Infinity, refetchOnWindowFocus: false, networkMode: "always",
  });
  return <details className="text-caption" onToggle={(event) => setOpen(event.currentTarget.open)}><summary>{t(($) => $.local_review.commits)}</summary>
    {open && <div className="max-h-28 overflow-auto py-2">
      {commits.isPending && <p role="status">{t(($) => $.local_review.loading)}</p>}
      {commits.error && <LocalReviewError error={commits.error} id={errorId} />}
      {commits.data?.pages.flatMap((page) => page.commits).map((commit) => <p key={commit.sha} className="break-all font-mono">{commit.sha.slice(0, 8)} {commit.subject}</p>)}
      {commits.hasNextPage && <Button variant="outline" disabled={commits.isFetchingNextPage} onClick={() => void commits.fetchNextPage()}>{t(($) => $.local_review.load_more_commits)}</Button>}
    </div>}
  </details>;
}
