"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { readGitBranches, readGitManifest, readReviewRepositories, readLocalIndex } from "../../platform/local-review-pages";
import { LocalReviewFileBrowser } from "./local-review-file-browser";
import { LocalReviewError } from "./local-review-error";
import { GitUncommittedChanges, GitCommitList } from "./git-live-changes";

export type GitChangesRequest = Omit<PagedReviewInput, "action">;
export type GitChangesScope = "uncommitted" | "branch" | "commits";

export function GitChangesPanel({ request, initialScope = "uncommitted", compact = false }: {
  readonly request: GitChangesRequest;
  readonly initialScope?: GitChangesScope;
  readonly compact?: boolean;
}) {
  const { t } = useT("issues");
  const [selected, setSelected] = useState("");
  const repositories = useQuery({
    queryKey: ["git-repositories", request.workspace_id, request.runtime_id, request.task_id, request.project_resource_id, request.path],
    queryFn: ({ signal }) => readReviewRepositories({ ...request, scope: "checkout" }, signal),
    retry: false, networkMode: "always", refetchOnWindowFocus: false,
  });
  const paths = repositories.isError ? [] : repositories.data?.repositories ?? [];
  const path = paths.includes(selected) ? selected : paths[0];
  return <div className="space-y-3">
    <p className="break-all text-caption">{t(($) => $.git_changes.directory)} · {request.path}</p>
    {repositories.isPending && <p role="status">{t(($) => $.local_review.loading)}</p>}
    {repositories.error && <><LocalReviewError error={repositories.error} id="git-repositories-error" /><Button size="sm" variant="outline" disabled={repositories.isFetching} onClick={() => void repositories.refetch()}>{t(($) => $.local_review.refresh)}</Button></>}
    {repositories.isSuccess && !paths.length && <p role="status">{t(($) => $.git_changes.no_git)}</p>}
    {paths.length > 1 && <label className="block space-y-1 text-caption">{t(($) => $.local_review.repository)}<select className="h-8 w-full rounded-md border bg-background px-2" value={path} onChange={(event) => setSelected(event.target.value)}>{paths.map((value) => <option key={value} value={value}>{value}</option>)}</select></label>}
    {path && <GitChangesSession key={JSON.stringify([request.workspace_id, request.runtime_id, request.task_id, request.project_resource_id, path])} request={{ ...request, path, target: path === request.path ? request.target : "", scope: "checkout" }} initialScope={initialScope} compact={compact} />}
  </div>;
}

function GitChangesSession({ request, initialScope, compact }: { readonly request: GitChangesRequest; readonly initialScope: GitChangesScope; readonly compact: boolean }) {
  const { t } = useT("issues");
  const [scope, setScope] = useState(initialScope);
  const index = useQuery({ queryKey: ["git-uncommitted", request], queryFn: ({ signal }) => readLocalIndex(request, signal), retry: false, networkMode: "always", refetchOnWindowFocus: false });
  const [selectedTarget, setSelectedTarget] = useState(request.target ?? "");
  const branches = useQuery({
    queryKey: ["git-branches", request],
    queryFn: ({ signal }) => readGitBranches(request, signal),
    retry: false, networkMode: "always", refetchOnWindowFocus: false,
  });
  const targets = branches.data?.branches ?? [];
  const target = targets.includes(selectedTarget) ? selectedTarget : ["refs/heads/main", "refs/remotes/origin/main", "main", "origin/main"].find((value) => targets.includes(value)) ?? targets[0] ?? "";
  const current = { ...request, target };
  const labels = {
    uncommitted: t(($) => $.git_changes.uncommitted),
    branch: t(($) => $.git_changes.branch),
    commits: t(($) => $.git_changes.commits),
  };
  return <section className="flex min-h-0 min-w-0 flex-col gap-3">
    <dl className="grid min-w-0 gap-1 text-caption">
      <div className="flex gap-2"><dt className="shrink-0 text-muted-foreground">{t(($) => $.git_changes.runtime)}</dt><dd className="break-all">{request.runtime_id || index.data?.runtime_id || "—"}</dd></div>
      <div className="flex gap-2"><dt className="shrink-0 text-muted-foreground">{t(($) => $.local_review.repository)}</dt><dd className="break-all">{request.path}</dd></div>
      <div className="flex gap-2"><dt className="shrink-0 text-muted-foreground">{t(($) => $.git_changes.current_branch)}</dt><dd className="break-all">{index.isError ? "—" : index.data?.status.branch || "—"}</dd></div>
    </dl>
    <div className="flex flex-wrap gap-1" aria-label={t(($) => $.git_changes.scope)}>
      {(["uncommitted", "branch", "commits"] as const).map((value) => <Button key={value} size="sm" variant={scope === value ? "secondary" : "ghost"} aria-pressed={scope === value} onClick={() => setScope(value)}>{labels[value]}</Button>)}
    </div>
    {scope === "branch" && <label className="flex flex-wrap items-center gap-2 text-caption">
      {t(($) => $.local_review.target)}
      <select className="h-8 max-w-full rounded-md border bg-background px-2" value={target} onChange={(event) => setSelectedTarget(event.target.value)} disabled={!targets.length}>
        {!targets.length && <option value="">{t(($) => $.local_review.target)}</option>}
        {targets.map((branch) => <option key={branch} value={branch}>{branch}</option>)}
      </select>
    </label>}
    {scope === "uncommitted" ? <GitUncommittedChanges request={request} compact={compact} /> : scope === "commits" ? <GitCommitList request={request} compact={compact} /> : branches.isPending ? <p role="status">{t(($) => $.local_review.loading)}</p> : branches.error ? <LocalReviewError error={branches.error} id="git-branches-error" /> : !target ? <p role="status">{t(($) => $.git_changes.no_git)}</p> : <GitComparison key={target} request={current} compact={compact} />}
  </section>;
}

function GitComparison({ request, compact }: { readonly request: GitChangesRequest; readonly compact: boolean }) {
  const { t } = useT("issues");
  const comparison = useQuery({
    queryKey: ["git-comparison", request],
    queryFn: ({ signal }) => readGitManifest({ ...request, git_scope: "branch" }, signal),
    retry: false, networkMode: "always", refetchOnWindowFocus: false,
  });
  const manifest = comparison.isError ? undefined : comparison.data;
  return <div className="space-y-3">
    <Button size="sm" variant="outline" disabled={comparison.isFetching} onClick={() => void comparison.refetch()}>{t(($) => $.local_review.refresh)}</Button>
    {comparison.isPending && <p role="status">{t(($) => $.local_review.loading)}</p>}
    {comparison.error && <LocalReviewError error={comparison.error} id="git-comparison-error" />}
    {manifest && <>
      <p className="break-all text-caption">{manifest.header.repository} · {manifest.header.branch}</p>
      <LocalReviewFileBrowser compact={compact} diffLayout={compact ? "unified" : "split"} key={manifest.version_id} request={request} manifest={manifest} />
    </>}
  </div>;
}
