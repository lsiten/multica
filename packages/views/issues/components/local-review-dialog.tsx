"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import { reviewDiffLines } from "@multica/core/types/local-review-diff";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { readLocalReview } from "../../platform/local-review";
import { useT } from "../../i18n";

export function LocalReviewDialog({ request, onClose }: { request: LocalReviewRequest; onClose: () => void }) {
  const { t } = useT("issues");
  const client = useQueryClient();
  const [target, setTarget] = useState(request.target);
  const [targetDraft, setTargetDraft] = useState(request.target);
  const [repositoryPath, setRepositoryPath] = useState(request.path);
  const runtimeKey = ["local-review-runtime", request.workspace_id, request.task_id];
  const runtimeIdentity = useQuery<string>({ queryKey: runtimeKey, enabled: false, initialData: request.runtime_id || undefined });
  const effectiveRequest = { ...request, runtime_id: request.runtime_id || runtimeIdentity.data, path: repositoryPath, review_id: repositoryPath === request.path ? request.review_id : undefined };
  const [selected, setSelected] = useState("");
  const [comment, setComment] = useState("");
  const [confirmMerge, setConfirmMerge] = useState(false);
  const key = ["local-review", request.workspace_id, request.task_id, repositoryPath, target];
  const snapshot = useQuery({
    queryKey: key,
    queryFn: async () => {
      const result = await readLocalReview({ ...effectiveRequest, target, review_id: effectiveRequest.review_id ?? client.getQueryData<Awaited<ReturnType<typeof readLocalReview>>>(key)?.review_id });
      if (result.runtime_id) client.setQueryData(runtimeKey, result.runtime_id);
      return result;
    },
    retry: false,
    refetchInterval: (query) => query.state.error ? false : ["queued", "running"].includes(query.state.data?.command_status ?? "") ? 3000 : 15000,
  });
  const operation = useMutation({
    mutationFn: (action: LocalReviewRequest["action"]) => {
      if (!snapshot.data) throw new Error("Load changes first");
      return readLocalReview({ ...effectiveRequest, runtime_id: effectiveRequest.runtime_id || snapshot.data.runtime_id, review_id: snapshot.data.review_id ?? effectiveRequest.review_id, target, action, snapshot_id: snapshot.data.id, comment });
    },
    onSuccess: (data) => { client.setQueryData(key, data); setConfirmMerge(false); },
  });
  const data = snapshot.data;
  const file = data?.files.find((file) => file.path === selected) ?? data?.files[0];
  const pending = snapshot.isPending || operation.isPending || data?.command_status === "queued" || data?.command_status === "running";
  const targetChanged = targetDraft.trim() !== target;
  const busy = pending || targetChanged;
  const merged = data?.review.state === "merged";
  const error = operation.error ?? snapshot.error;
  const errorMessage = error?.message === "local_review_upgrade_required" ? t(($) => $.local_review.upgrade_required) : error?.name === "TimeoutError" ? t(($) => $.local_review.request_timeout) : error?.message;
  return <Dialog open onOpenChange={(open) => { if (!open && !operation.isPending) onClose(); }}>
    <DialogContent className="flex h-[85vh] w-[95vw] max-w-6xl flex-col sm:max-w-6xl">
      <DialogHeader>
        <DialogTitle>{t(($) => $.local_review.title)}</DialogTitle>
        <DialogDescription className="break-all">{repositoryPath}</DialogDescription>
      </DialogHeader>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-caption">{data?.branch ?? "—"} →</span>
        <Input aria-label={t(($) => $.local_review.target)} className="w-48" value={targetDraft} disabled={operation.isPending || !!effectiveRequest.review_id} onChange={(event) => { setTargetDraft(event.target.value); setConfirmMerge(false); }} list="local-review-branches" />
        <datalist id="local-review-branches">{data?.branches.map((branch) => <option key={branch} value={branch} />)}</datalist>
        <Button variant="outline" disabled={pending || !targetDraft.trim()} onClick={() => { setConfirmMerge(false); if (targetChanged) setTarget(targetDraft.trim()); else if (!data) void snapshot.refetch(); else operation.mutate("read"); }}>{targetChanged ? t(($) => $.local_review.apply_target) : t(($) => $.local_review.refresh)}</Button>
        {data && <span className="text-caption">{data.review.state === "open" ? t(($) => $.local_review.state_open) : t(($) => $.local_review[data.review.state])} · {data.head.slice(0, 8)} · {data.files.length} {t(($) => $.local_review.files)}</span>}
      </div>
      {errorMessage && <p role="alert" className="text-caption text-destructive">{errorMessage}</p>}
      {data?.command_error && <p role="alert" className="text-caption text-destructive">{data.command_error}</p>}
      {(data?.command_status === "queued" || data?.command_status === "running") && <p role="status" className="text-caption">{t(($) => $.local_review.runtime_pending)}</p>}
      {snapshot.isPending && <p role="status">{t(($) => $.local_review.loading)}</p>}
      {data && <>
        {data.repositories.length > 1 && <select className="rounded border bg-background p-2 text-caption" aria-label={t(($) => $.local_review.repository)} value="" onChange={(event) => { if (event.target.value) { setRepositoryPath(event.target.value); setConfirmMerge(false); } }}><option value="">{t(($) => $.local_review.select_repository)}</option>{data.repositories.map((path) => <option key={path} value={path}>{path}</option>)}</select>}
        {data.dirty && <p className="text-caption text-warning">{t(($) => $.local_review.dirty)}</p>}
        <div className="grid min-h-0 flex-1 grid-cols-1 grid-rows-[auto_minmax(0,1fr)] gap-3 md:grid-cols-[16rem_1fr] md:grid-rows-1">
          <nav className="max-h-36 overflow-auto rounded border md:max-h-none" aria-label={t(($) => $.local_review.files)}>
            {data.files.map((entry) => <button key={entry.path} type="button" onClick={() => setSelected(entry.path)} className={`block w-full break-all px-3 py-2 text-left text-caption hover:bg-accent ${file?.path === entry.path ? "bg-accent font-semibold" : ""}`}>{entry.path}</button>)}
          </nav>
          <div className="min-h-0 overflow-auto rounded border font-mono text-caption">
            {file ? <><p className="sticky top-0 border-b bg-card p-2 font-semibold">{file.path}</p><pre className="min-w-max p-2">{reviewDiffLines(file.patch, file.status === "untracked").map((line, index) => <div key={index} className={line.kind === "add" ? "bg-success/10 text-success" : line.kind === "remove" ? "bg-destructive/10 text-destructive" : ""}><span className="mr-2 inline-block w-10 select-none text-right text-muted-foreground">{line.oldLine ?? ""}</span><span className="mr-3 inline-block w-10 select-none text-right text-muted-foreground">{line.newLine ?? ""}</span>{line.text || " "}</div>)}</pre></> : <p className="p-3">{t(($) => $.local_review.empty)}</p>}
          </div>
        </div>
        <details className="text-caption"><summary>{t(($) => $.local_review.commits)}</summary><pre className="max-h-28 overflow-auto whitespace-pre-wrap">{data.commits || "—"}</pre></details>
        {!!data.history?.length && <details className="text-caption"><summary>{t(($) => $.local_review.history)}</summary><ol className="max-h-32 space-y-2 overflow-auto py-2">{data.history.map((event, index) => <li key={index} className="rounded border p-2"><p>{event.actor_name || t(($) => $.local_review.former_member)} · {event.kind === "approve" ? t(($) => $.local_review.approved) : event.kind === "request_changes" ? t(($) => $.local_review.changes_requested) : event.kind === "merge" || event.kind === "merge_recovered" ? t(($) => $.local_review.merged) : t(($) => $.local_review.submit)} · <time dateTime={event.created_at}>{new Date(event.created_at).toLocaleString()}</time></p>{event.comment && <p className="mt-1 whitespace-pre-wrap break-words">{event.comment}</p>}</li>)}</ol></details>}
        <Input value={comment} onChange={(event) => setComment(event.target.value)} placeholder={t(($) => $.local_review.comment)} aria-label={t(($) => $.local_review.comment)} maxLength={8000} />
        <div className="flex flex-wrap justify-end gap-2">
          <Button variant="outline" disabled={busy || merged || !data.head} onClick={() => operation.mutate("submit")}>{t(($) => $.local_review.submit)}</Button>
          <Button variant="outline" disabled={busy || merged || !data.head} onClick={() => operation.mutate("request_changes")}>{t(($) => $.local_review.request_changes)}</Button>
          <Button variant="outline" disabled={busy || merged || !data.head} onClick={() => operation.mutate("approve")}>{t(($) => $.local_review.approve)}</Button>
          <Button disabled={busy || merged || data.dirty || data.branch === data.target || data.review.state !== "approved"} onClick={() => setConfirmMerge(true)}>{t(($) => $.local_review.merge)}</Button>
        </div>
        {confirmMerge && <div role="alert" className="flex flex-wrap items-center justify-between gap-2 rounded border p-3 text-caption">
          <span>{data.branch} ({data.head.slice(0, 8)}) → {target} ({data.target_head.slice(0, 8)})</span>
          <Button disabled={busy} onClick={() => operation.mutate("merge")}>{t(($) => $.local_review.confirm_merge)}</Button>
          <Button variant="ghost" onClick={() => setConfirmMerge(false)}>{t(($) => $.local_review.cancel)}</Button>
        </div>}
        {merged && <p role="status" className="text-caption text-success">{t(($) => $.local_review.merged)} {data.review.merged_commit}</p>}
      </>}
    </DialogContent>
  </Dialog>;
}
