"use client";
import { MirrorWindowSelection } from "./mirror-window-selection";
import { TranscriptButton } from "../../../common/task-transcript";
import type { VscreenIntervention } from "@multica/core/api";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getApi, vscreenErrorReason } from "@multica/core/api";
import { vscreenInterventionsOptions, vscreenKeys } from "@multica/core/runtimes";
import type { VscreenSourceDescriptor, VscreenScope, VscreenStateSnapshot } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../../../i18n";
import { useMirrorPlatform } from "./mirror-platform";

export function MirrorHandoff({ scope, owner, online, catalog, canRequest, permissions, onRequest }: {
  readonly scope: VscreenScope; readonly owner: boolean; readonly online: boolean;
  readonly permissions?: VscreenStateSnapshot["permissions"]; readonly canRequest: boolean; readonly catalog: readonly VscreenSourceDescriptor[]; readonly onRequest: () => void;
}) {
  const { t } = useT("runtimes");
  const platform = useMirrorPlatform();
  const cache = useQueryClient();
  const query = useQuery({ ...vscreenInterventionsOptions(scope), enabled: owner });
  const local = useQuery({ queryKey: [...vscreenKeys.all(scope), "local-owner"], queryFn: () => platform.localControl!(scope, { action: "status" }), enabled: owner && online && !!platform.localControl, retry: false, refetchInterval: 5000 });
  const [summary, setSummary] = useState("");
  const [destination, setDestination] = useState("");
  const row = query.data?.find((entry) => ["awaiting_takeover", "human", "ready_to_continue", "stale"].includes(entry.state)) ?? query.data?.[0];
  const api = getApi().vscreen(scope);
  const mutation = useMutation({
    mutationFn: async (action: "takeover" | "return" | "continue" | "fresh" | "cancel") => {
      if (!row) throw new Error("intervention_failed");
      if (action === "continue" || action === "fresh") return api.interventions.continue(row, summary, action === "fresh");
      if (action === "cancel") return api.interventions.cancel(row);
      if (!platform.localControl || !local.data?.ok) throw new Error("local_owner_required");
      const result = await platform.localControl(scope, action === "takeover" ? { action, interventionId: row.id, destinationSourceId: destination } : { action, interventionId: row.id, summary });
      if (!result.ok) throw new Error(result.reason ?? "handoff_failed");
    }, retry: false,
    onSettled: () => cache.invalidateQueries({ queryKey: vscreenKeys.all(scope) }),
  });
  if (!owner) return null;
  const error = vscreenErrorReason(mutation.error) ?? mutation.error?.message;
  const pendingReport = error === "report_pending" || local.data?.reason === "report_pending";
  const tooLong = new TextEncoder().encode(summary).length > 2048;
  const disabled = mutation.isPending || !online || tooLong || pendingReport;
  const localOwner = local.data?.local === true && (local.data.ok || local.data.reason === "report_pending");
  const selectionRequired = row?.id === local.data?.interventionId && local.data?.selectionRequired === true;
  const physical = catalog.filter((source) => source.source.kind !== "virtual");
  const permissionLabel = (value: string | undefined) => value === "granted" ? t(($) => $.vscreen.handoff.granted) : value === "denied" || value === "restricted" || value === "not_determined" ? t(($) => $.vscreen.handoff.not_granted) : t(($) => $.vscreen.handoff.unknown);
  const labels: Record<string, string> = {
    awaiting_takeover: t(($) => $.vscreen.handoff.awaiting), human: t(($) => $.vscreen.handoff.human),
    ready_to_continue: t(($) => $.vscreen.handoff.ready), continued: t(($) => $.vscreen.handoff.continued), stale: t(($) => $.vscreen.handoff.stale), cancelled: t(($) => $.vscreen.handoff.cancelled),
  };
  return <section className="space-y-3 border-t p-3" aria-label={t(($) => $.vscreen.handoff.title)}>
    <p className="text-caption" role="status">{row ? labels[row.state] ?? t(($) => $.vscreen.handoff.stale) : t(($) => $.vscreen.handoff.remote)}</p>
    {row && <div className="flex flex-wrap gap-2 text-caption"><span>{t(($) => $.vscreen.handoff.source_run)}: <InterventionRun scope={scope} row={row} id={row.sourceTaskId} label={t(($) => $.vscreen.handoff.source_run)} /></span>{row.continuationTaskId && <span>{t(($) => $.vscreen.handoff.child_run)}: <InterventionRun scope={scope} row={row} id={row.continuationTaskId} label={t(($) => $.vscreen.handoff.child_run)} /></span>}</div>}
    {canRequest && (!row || ["continued", "cancelled", "stale"].includes(row.state)) && <Button size="sm" variant="outline" disabled={disabled} onClick={onRequest}>{t(($) => $.vscreen.takeover)}</Button>}
    {row?.state === "awaiting_takeover" && localOwner && selectionRequired && platform.localControl && <MirrorWindowSelection key={JSON.stringify([scope,row.id])} scope={scope} interventionId={row.id} disabled={disabled} control={platform.localControl} onAdopted={async () => { await query.refetch(); await local.refetch(); }} />}
    {row?.state === "awaiting_takeover" && localOwner && !selectionRequired && <div className="flex flex-wrap items-center gap-2">
      <label className="text-caption">{t(($) => $.vscreen.handoff.destination)}<select className="ml-2 max-w-full rounded border bg-background p-1" value={destination} onChange={(e) => setDestination(e.target.value)}><option value="">{t(($) => $.vscreen.choose_source)}</option>{physical.map((source) => <option key={source.source.sourceId} value={source.source.sourceId}>{source.name || t(($) => $.vscreen.physical)}</option>)}</select></label>
      <Button size="sm" disabled={disabled || !physical.some((source) => source.source.sourceId === destination)} aria-busy={mutation.isPending} onClick={() => mutation.mutate("takeover")}>{t(($) => $.vscreen.handoff.move_here)}</Button>
    </div>}
    {row && ["human", "ready_to_continue"].includes(row.state) && <label className="block space-y-1 text-caption">{t(($) => $.vscreen.handoff.summary)}<Textarea value={summary} onChange={(e) => setSummary(e.target.value)} disabled={mutation.isPending} /><span className={tooLong ? "text-destructive" : "text-muted-foreground"}>{t(($) => $.vscreen.handoff.summary_limit)}</span></label>}
    <div className="flex flex-wrap gap-2">
      {row?.state === "human" && localOwner && <Button size="sm" disabled={disabled} aria-busy={mutation.isPending} onClick={() => mutation.mutate("return")}>{t(($) => $.vscreen.handoff.return)}</Button>}
      {row?.state === "ready_to_continue" && <Button size="sm" disabled={disabled || !row.returnReceiptId} aria-busy={mutation.isPending} onClick={() => mutation.mutate("continue")}>{t(($) => $.vscreen.handoff.continue)}</Button>}
      {row?.state === "ready_to_continue" && error === "resume_unavailable" && <Button size="sm" variant="outline" disabled={disabled} onClick={() => mutation.mutate("fresh")}>{t(($) => $.vscreen.handoff.fresh)}</Button>}
      {row && ["awaiting_takeover", "human", "ready_to_continue", "stale"].includes(row.state) && <Button size="sm" variant="ghost" disabled={mutation.isPending} onClick={() => mutation.mutate("cancel")}>{t(($) => $.vscreen.handoff.cancel)}</Button>}
    </div>
    {(error || query.isError) && <p role={pendingReport ? "status" : "alert"} className="text-caption text-muted-foreground">{pendingReport ? t(($) => $.vscreen.handoff.report_pending) : error === "resume_unavailable" ? t(($) => $.vscreen.handoff.resume_unavailable) : t(($) => $.vscreen.handoff.failed)}</p>}
    {pendingReport && <Button size="sm" variant="ghost" onClick={async () => { await query.refetch(); const status = await local.refetch(); if (status.data?.ok) mutation.reset(); }}>{t(($) => $.vscreen.handoff.refresh)}</Button>}
    {localOwner && <p className="text-caption text-muted-foreground">{t(($) => $.vscreen.handoff.permissions, { accessibility: permissionLabel(permissions?.accessibility), screenRecording: permissionLabel(permissions?.screenRecording) })}</p>}
    {localOwner && <div className="flex flex-wrap gap-2"><Button size="sm" variant="link" onClick={() => void platform.localControl?.(scope, { action: "settings", permission: "screenRecording" })}>{t(($) => $.vscreen.handoff.screen_settings)}</Button><Button size="sm" variant="link" onClick={() => void platform.localControl?.(scope, { action: "settings", permission: "accessibility" })}>{t(($) => $.vscreen.handoff.ax_settings)}</Button></div>}
  </section>;
}

function InterventionRun({ scope, row, id, label }: { scope: VscreenScope; row: VscreenIntervention; id: string; label: string }) {
  const [enabled, setEnabled] = useState(false);
  const run = useQuery({ queryKey: [...vscreenKeys.all(scope), "intervention-run", id], queryFn: () => getApi().vscreen(scope).interventions.run(row,id), enabled, retry: false });
  return <><Button size="sm" variant="link" disabled={run.isFetching} onClick={() => { setEnabled(true); if (run.isError) void run.refetch(); }}>{id.slice(0,8)}</Button>{run.data && <TranscriptButton task={run.data} agentName={label} title={label} renderButton={false} open={enabled} onOpenChange={setEnabled} />}{run.isError && <span role="alert">{label}: {id}</span>}</>;
}
