"use client";

import { useEffect, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueProgressOptions } from "@multica/core/issue-progress";
import { Button } from "@multica/ui/components/ui/button";
import { useNavigation } from "../../navigation";
import { WorkProgressPanel } from "./work-progress-panel";
import { WorkProgressEntry, WorkProgressSummary } from "./work-progress-entry";
import { useWorkProgressLabels } from "./work-progress-labels";

export function IssueWorkProgress({ issueId, projectId, hasChildren, children }: { issueId: string; projectId: string | null; hasChildren: boolean; children: ReactNode }) {
  const { t } = useWorkProgressLabels();
  const workspaceId = useWorkspaceId();
  const memberId = useAuthStore(state => state.user?.id) ?? "";
  const query = useQuery(issueProgressOptions(workspaceId, memberId, issueId));
  const navigation = useNavigation();
  const routeRemaining = navigation.searchParams.get("work_view") === "remaining";
  const [remaining, setRemainingValue] = useState(routeRemaining);
  useEffect(() => { setRemainingValue(routeRemaining); }, [routeRemaining]);
  const setRemaining = (value: boolean) => {
    setRemainingValue(value);
    const search = new URLSearchParams(navigation.searchParams);
    if (value) search.set("work_view", "remaining"); else search.delete("work_view");
    const encoded = search.toString(); navigation.replace(`${navigation.pathname}${encoded ? `?${encoded}` : ""}${navigation.hash}`);
  };
  const view = query.data;
  const parent = hasChildren || !!view && view.summary.total > 0;
  const rootContext = view?.root && view.root.reasons.some(reason => !["unassigned", "ready", "waiting_children"].includes(reason));
  return <div className="mt-8 space-y-3" data-issue-work-progress>
    {query.isPending && <p role="status" className="text-caption text-muted-foreground">{t($ => $.work_progress.loading)}</p>}
    {query.isError && <div role="alert" className="flex flex-wrap items-center gap-2 text-caption"><span>{t($ => $.work_progress.unavailable)}</span><Button size="sm" variant="outline" onClick={() => void query.refetch()}>{t($ => $.work_progress.retry)}</Button></div>}
    {view && parent && <><p className="text-caption font-medium">{t($ => $.work_progress.all_descendants)}</p><WorkProgressSummary summary={view.summary} complete={view.complete} /></>}
    {view && !view.complete && <p role="status" className="text-caption text-destructive">{t($ => $.work_progress.incomplete)}</p>}
    {view?.root?.attention && rootContext && <WorkProgressEntry entry={view.root} currentProjectId={projectId} requestLinksOnly />}
    {parent && <div className="flex flex-wrap gap-1" aria-label={t($ => $.work_progress.view)}><Button size="sm" variant={!remaining ? "secondary" : "ghost"} aria-pressed={!remaining} onClick={() => setRemaining(false)}>{t($ => $.work_progress.direct_children)}</Button><Button size="sm" variant={remaining ? "secondary" : "ghost"} aria-pressed={remaining} onClick={() => setRemaining(true)}>{t($ => $.work_progress.remaining)}</Button></div>}
    {remaining ? <WorkProgressPanel scope={{ type: "issue", id: issueId }} currentProjectId={projectId} showSummary={false} /> : children}
  </div>;
}
