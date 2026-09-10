import { useEffect, useId, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import type { ReviewManifest } from "@multica/core/types/local-review-pages";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { mergeSelectedFiles, supportsSelectedMerge } from "../../platform/local-review-selection";
import { useT } from "../../i18n";
import { LocalReviewError } from "./local-review-error";

export function LocalReviewSelection({ request, manifest, paths, disabled, onBusyChange, onMerged, embedded = false }: {
  request: LocalReviewRequest; manifest: ReviewManifest; paths: string[]; disabled: boolean; onBusyChange: (busy: boolean) => void; onMerged: (commit: string) => void; embedded?: boolean;
}) {
  const { t } = useT("issues");
  const errorId = useId();
  const [message, setMessage] = useState("");
  const selectionKey = JSON.stringify([...paths].sort());
  const [confirmedSelection, setConfirmedSelection] = useState<string | null>(null);
  const confirm = confirmedSelection === selectionKey;
  const capability = useQuery({ queryKey: ["selected-merge-capability", request.workspace_id, request.task_id, request.runtime_id], queryFn: () => supportsSelectedMerge(request), retry: false, networkMode: "always" });
  const operation = useMutation({ networkMode: "always", mutationFn: () => mergeSelectedFiles({ ...request, target: manifest.header.target, version_id: manifest.version_id, paths, message }), onSuccess: (result) => { setConfirmedSelection(null); if (result.commit) onMerged(result.commit); } });
  useEffect(() => { onBusyChange(operation.isPending); return () => onBusyChange(false); }, [operation.isPending, onBusyChange]);
  const busy = disabled || operation.isPending;
  const conflicts = operation.data && JSON.stringify([...operation.data.paths].sort()) === selectionKey ? operation.data.conflicts : [];
  const error = operation.error ?? capability.error;
  return <section className={embedded ? "space-y-2 text-caption" : "space-y-2 rounded border p-3 text-caption"}>
    <p className="font-semibold">{t(($) => $.local_review.pending_merge)} · {paths.length} → {manifest.header.target}</p>
    <p className="text-muted-foreground">{t(($) => $.local_review.selection_hint)}</p>
    {capability.data === false && <p role="status">{t(($) => $.local_review.selection_upgrade)}</p>}
    {error && <LocalReviewError error={error} id={errorId} />}
    {!!conflicts.length && <div role="alert" className="rounded border border-destructive/30 p-2"><p>{t(($) => $.local_review.selection_conflict)}</p><ul className="max-h-28 overflow-auto">{conflicts.map((path) => <li key={path} className="break-all font-mono">{path}</li>)}</ul></div>}
    <Input aria-label={t(($) => $.local_review.selection_message)} placeholder={t(($) => $.local_review.selection_message)} value={message} disabled={busy || confirm} onChange={(event) => setMessage(event.target.value)} maxLength={8000} />
    {!confirm ? <Button disabled={busy || capability.data !== true || !paths.length || !message.trim() || new TextEncoder().encode(message).length > 8000 || manifest.header.dirty} onClick={() => setConfirmedSelection(selectionKey)}>{t(($) => $.local_review.merge_selection)}</Button> : <div className="space-y-2"><p className="break-all font-mono">{manifest.header.head.slice(0, 12)} → {manifest.header.target_head.slice(0, 12)}</p><div className="flex gap-2"><Button disabled={busy} onClick={() => operation.mutate()}>{t(($) => $.local_review.confirm_selection, { count: paths.length, target: manifest.header.target })}</Button><Button variant="outline" disabled={busy} onClick={() => setConfirmedSelection(null)}>{t(($) => $.local_review.cancel_commit)}</Button></div></div>}
  </section>;
}
