import { useEffect, useId, useMemo, useState } from "react";
import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import { useAuthStore } from "@multica/core/auth";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { readLocalIndex, changeLocalIndex, renewReviewLease } from "../../platform/local-review-pages";
import { useT } from "../../i18n";
import { LocalReviewError } from "./local-review-error";

type IndexAction = { readonly action: "stage" | "unstage" | "commit"; readonly paths?: string[] };
type Props = { readonly request: LocalReviewRequest; readonly onChanged: () => void; readonly onBusyChange: (busy: boolean) => void; readonly disabled?: boolean };

export function LocalReviewIndex({ request, onChanged, onBusyChange, disabled = false }: Props) {
  const { t } = useT("issues");
  const errorId = useId();
  const userId = useAuthStore((state) => state.user?.id);
  const client = useQueryClient();
  const key = useMemo(() => ["local-index", userId, request.workspace_id, request.task_id, request.path], [userId, request.workspace_id, request.task_id, request.path]);
  const writing = useIsMutating({ mutationKey: key }) > 0;
  const [message, setMessage] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [visible, setVisible] = useState(100);
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => readLocalIndex({ ...request, action: "index" }, signal), networkMode: "always", retry: false, refetchOnWindowFocus: false });
  const operation = useMutation({
    mutationKey: key,
    networkMode: "always",
    mutationFn: ({ action, paths }: IndexAction) => {
      const data = query.data;
      if (!data) throw new Error("Load Git index before modifying it");
      return changeLocalIndex({ ...request, runtime_id: request.runtime_id || data.runtime_id, action, target: data.status.branch, branch: data.status.branch, head: data.status.head, index_id: data.status.index_id, version_id: data.version_id, snapshot_id: data.version_id, paths, message: action === "commit" ? message : undefined });
    },
    onSuccess: async (_result, action) => {
      setConfirm(false);
      if (action.action === "commit") setMessage("");
      await client.invalidateQueries({ queryKey: key, exact: true });
      onChanged();
    },
  });
  useEffect(() => { onBusyChange(writing); return () => onBusyChange(client.isMutating({ mutationKey: key }) > 0); }, [writing, onBusyChange, client, key]);
  const data = query.data;
  const lease = useQuery({
    queryKey: ["local-index-lease", userId, request.workspace_id, request.task_id, request.path, data?.version_id],
    queryFn: ({ signal }) => {
      if (!data) throw new Error("Index snapshot unavailable");
      return renewReviewLease({ ...request, action: "lease", runtime_id: request.runtime_id || data.runtime_id, target: data.status.branch, version_id: data.version_id }, signal);
    },
    enabled: !!data, networkMode: "always", retry: false, refetchInterval: 60000, refetchIntervalInBackground: true,
  });
  const staged = data?.status.files.filter((file) => file.staged) ?? [];
  const unstaged = data?.status.files.filter((file) => file.unstaged) ?? [];
  const busy = disabled || query.isFetching || writing;
  const error = operation.error ?? query.error ?? lease.error;
  const invalidMessage = !message.trim() || new TextEncoder().encode(message).length > 8000;
  return <section className="space-y-2 rounded border p-3 text-caption">
    <div className="flex items-center justify-between gap-2"><span className="break-all font-mono">{data?.status.branch}</span><Button variant="outline" disabled={busy} onClick={() => { setConfirm(false); void query.refetch(); }}>{t(($) => $.local_review.refresh)}</Button></div>
    {query.isPending && <p role="status">{t(($) => $.local_review.loading)}</p>}
    {error && <LocalReviewError error={error} id={errorId} />}
    {data && <>
      <p className="text-muted-foreground">{t(($) => $.local_review.index_hint)}</p>
      <div className="grid max-h-48 gap-3 overflow-auto md:grid-cols-2">
        {([{ action: "stage", files: unstaged, title: t(($) => $.local_review.unstaged) }, { action: "unstage", files: staged, title: t(($) => $.local_review.staged) }] as const).map((group) => <div key={group.action}>
          <h4 className="mb-1 font-semibold">{group.title} · {group.files.length}</h4>
          {group.files.slice(0, visible).map((file) => <div key={file.path} className="flex items-start gap-2 py-1"><span className="min-w-0 flex-1 break-all font-mono">{file.path}{(file.conflicted || file.unsupported) && <span className="block text-warning">{t(($) => $.local_review.index_unavailable)}</span>}</span><Button variant="ghost" disabled={busy || confirm || file.conflicted || file.unsupported} aria-label={group.action === "stage" ? t(($) => $.local_review.stage_file, { path: file.path }) : t(($) => $.local_review.unstage_file, { path: file.path })} onClick={() => operation.mutate({ action: group.action, paths: [file.path] })}>{group.action === "stage" ? t(($) => $.local_review.stage) : t(($) => $.local_review.unstage)}</Button></div>)}
          {group.files.length > visible && <Button variant="ghost" onClick={() => setVisible(visible + 100)}>{t(($) => $.local_review.load_more_files)}</Button>}
        </div>)}
      </div>
      <Input aria-label={t(($) => $.local_review.commit_message)} placeholder={t(($) => $.local_review.commit_message)} value={message} disabled={busy || confirm} maxLength={8000} onChange={(event) => setMessage(event.target.value)} />
      {!confirm ? <Button disabled={busy || !staged.length || invalidMessage || staged.some((file) => file.conflicted || file.unsupported)} onClick={() => setConfirm(true)}>{t(($) => $.local_review.create_commit)}</Button> : <div className="flex flex-wrap gap-2"><Button disabled={busy} onClick={() => operation.mutate({ action: "commit" })}>{t(($) => $.local_review.confirm_commit, { count: staged.length })}</Button><Button variant="outline" disabled={busy} onClick={() => setConfirm(false)}>{t(($) => $.local_review.cancel_commit)}</Button></div>}
    </>}
  </section>;
}
