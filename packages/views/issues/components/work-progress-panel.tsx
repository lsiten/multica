"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAuthStore } from "@multica/core/auth";
import { errorCode } from "@multica/core/api";
import { workProgressInfiniteOptions } from "@multica/core/issue-progress";
import type { ProgressEntry, ProgressFilter, ProgressScope } from "@multica/core/types/issue-progress";
import { memberListOptions, agentListOptions, squadListOptions } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { AppLink, useNavigation } from "../../navigation";
import { useWorkspacePaths } from "@multica/core/paths";
import { HumanRequestCard } from "../../common/human-request-card";
import { useRestoredScrollOffset } from "../../platform";
import { useIssueDetailScrollRestore } from "../hooks/use-issue-detail-scroll-restore";
import { WorkProgressEntry, WorkProgressSummary } from "./work-progress-entry";
import { useWorkProgressLabels } from "./work-progress-labels";

const FILTERS: ProgressFilter[] = ["all", "blocked", "review", "follow_up", "ready", "awaiting_agent", "other_member", "prerequisites"];

export function WorkProgressPanel({ scope, currentProjectId, showSummary = true }: { scope: ProgressScope; currentProjectId?: string | null; showSummary?: boolean }) {
  const { t } = useWorkProgressLabels();
  const workspaceId = useWorkspaceId();
  const memberId = useAuthStore(state => state.user?.id) ?? "";
  const navigation = useNavigation();
  const client = useQueryClient();
  const routeFilter = navigation.searchParams.get("work_filter") ?? "";
  const routeAssignee = navigation.searchParams.get("work_assignee") ?? "";
  const routeMine = navigation.searchParams.get("work_mine") === "true";
  const [selection, setSelection] = useState({ filter: routeFilter, assignee: routeAssignee, mine: routeMine });
  useEffect(() => { setSelection({ filter: routeFilter, assignee: routeAssignee, mine: routeMine }); }, [routeFilter, routeAssignee, routeMine]);
  const filter = FILTERS.find(value => value === selection.filter) ?? "all";
  const assignee = selection.assignee;
  const [assigneeType, assigneeId] = assignee.split(":");
  const mine = selection.mine;
  const validAssigneeType = assigneeType === "agent" || assigneeType === "member" || assigneeType === "squad" ? assigneeType : undefined;
  const options = workProgressInfiniteOptions(workspaceId, memberId, scope, { filter, mine, ...(validAssigneeType && assigneeId ? { assignee_type: validAssigneeType, assignee_id: assigneeId } : {}) });
  const query = useInfiniteQuery(options);
  const members = useQuery(memberListOptions(workspaceId));
  const agents = useQuery(agentListOptions(workspaceId));
  const squads = useQuery(squadListOptions(workspaceId));
  const paths = useWorkspacePaths();
  const scrollKey = `work-progress:${scope.type}:${scope.id}`;
  const restoredTop = useRestoredScrollOffset(scrollKey);
  const [scrollContainer, setScrollContainer] = useState<HTMLElement | null>(null);
  useIssueDetailScrollRestore({ restoreKey: `${workspaceId}:${memberId}:${scrollKey}:${filter}:${assignee}:${mine}`, scrollContainerEl: scrollContainer, ready: !!query.data, disabled: scope.type !== "project", overrideTop: restoredTop });
  const handledError = useRef<Error | null>(null);
  useEffect(() => {
    if (query.error && errorCode(query.error) === "progress_snapshot_changed" && handledError.current !== query.error) {
      handledError.current = query.error;
      void client.resetQueries({ queryKey: options.queryKey, exact: true });
    }
  }, [client, options.queryKey, query.error]);
  const updateFilter = (key: string, value: string) => {
    const next = { ...selection };
    if (key === "work_filter") next.filter = value;
    if (key === "work_assignee") next.assignee = value;
    if (key === "work_mine") next.mine = value === "true";
    setSelection(next);
    const search = new URLSearchParams(navigation.searchParams);
    const parameters: [string, string][] = [["work_filter", next.filter], ["work_assignee", next.assignee], ["work_mine", next.mine ? "true" : ""]];
    for (const [name, selected] of parameters) {
      if (selected) search.set(name, selected); else search.delete(name);
    }
    const encoded = search.toString();
    navigation.replace(`${navigation.pathname}${encoded ? `?${encoded}` : ""}${navigation.hash}`);
  };
  const view = query.data?.pages[0];
  const entries = useMemo(() => {
    const unique = new Map<string, ProgressEntry>();
    for (const page of query.data?.pages ?? []) {
      if (page.version !== query.data?.pages[0]?.version) continue;
      for (const entry of page.items) unique.set(entry.issue.id, entry);
    }
    return [...unique.values()];
  }, [query.data]);
  const groups = useMemo(() => {
    const grouped = new Map<string, ProgressEntry[]>();
    for (const entry of entries) { const key = entry.group?.id ?? entry.issue.id; const group = grouped.get(key); if (group) group.push(entry); else grouped.set(key, [entry]); }
    return [...grouped.values()];
  }, [entries]);
  const filterLabels: Record<ProgressFilter, string> = { all: t($ => $.work_progress.filters.all), blocked: t($ => $.work_progress.filters.blocked), review: t($ => $.work_progress.filters.review), follow_up: t($ => $.work_progress.filters.follow_up), ready: t($ => $.work_progress.filters.ready), awaiting_agent: t($ => $.work_progress.filters.awaiting_agent), other_member: t($ => $.work_progress.filters.other_member), prerequisites: t($ => $.work_progress.filters.prerequisites) };
  return <section ref={setScrollContainer} data-tab-scroll-root={scope.type === "project" ? scrollKey : undefined} className={`min-h-0 space-y-4 ${scope.type === "project" ? "flex-1 overflow-y-auto" : ""}`} aria-label={scope.type === "project" ? t($ => $.work_progress.attention) : t($ => $.work_progress.remaining)}>
    <div className="flex flex-wrap items-center gap-2" aria-label={t($ => $.work_progress.filters_label)}>
      {FILTERS.map(value => <Button key={value} size="sm" variant={filter === value ? "secondary" : "ghost"} aria-pressed={filter === value} onClick={() => updateFilter("work_filter", value === "all" ? "" : value)}>{filterLabels[value]}</Button>)}
      <label className="flex min-w-0 items-center gap-2 text-caption"><span>{t($ => $.work_progress.assignee)}</span><select className="h-[var(--button-height-sm)] max-w-48 rounded-md border bg-background px-2 text-caption focus-visible:outline focus-visible:outline-ring" value={assignee} onChange={event => updateFilter("work_assignee", event.target.value)}><option value="">{t($ => $.work_progress.all_assignees)}</option><optgroup label={t($ => $.work_progress.members)}>{members.data?.map(member => <option key={member.user_id} value={`member:${member.user_id}`}>{member.name}</option>)}</optgroup><optgroup label={t($ => $.work_progress.agents)}>{agents.data?.map(agent => <option key={agent.id} value={`agent:${agent.id}`}>{agent.name}</option>)}</optgroup><optgroup label={t($ => $.work_progress.squads)}>{squads.data?.map(squad => <option key={squad.id} value={`squad:${squad.id}`}>{squad.name}</option>)}</optgroup></select></label>
      <label className="flex items-center gap-2 text-caption"><input type="checkbox" checked={mine} onChange={event => updateFilter("work_mine", event.target.checked ? "true" : "")} />{t($ => $.work_progress.mine)}</label>
      <Button size="sm" variant="outline" disabled={query.isFetching} aria-busy={query.isFetching} onClick={() => void query.refetch()}>{t($ => $.work_progress.refresh)}</Button>
    </div>
    {view && showSummary && <WorkProgressSummary summary={view.summary} complete={view.complete} />}
    {view && !view.complete && <p role="status" className="break-words text-caption text-destructive">{t($ => $.work_progress.incomplete)}</p>}
    {query.isPending && <p role="status" className="text-caption text-muted-foreground">{t($ => $.work_progress.loading)}</p>}
    {query.isError && <div role="alert" className="space-y-2 text-caption"><p>{t($ => $.work_progress.unavailable)}</p><Button size="sm" variant="outline" onClick={() => void query.refetch()}>{t($ => $.work_progress.retry)}</Button></div>}
    {view && showSummary && view.root?.attention && <WorkProgressEntry entry={view.root} currentProjectId={currentProjectId} />}
    {view?.project_requests.filter(request => !mine || request.needs_me).map(request => <HumanRequestCard key={request.id} requestId={request.id} />)}
    {view && entries.length === 0 && !view.project_requests.some(request => !mine || request.needs_me) && <p className="text-caption text-muted-foreground">{!view.complete ? t($ => $.work_progress.incomplete) : filter !== "all" || mine || assignee ? t($ => $.work_progress.no_match) : scope.type === "project" ? t($ => $.work_progress.no_attention) : view.summary.open === 0 ? t($ => $.work_progress.no_remaining) : t($ => $.work_progress.no_match)}</p>}
    {groups.map(group => {
      const ancestor = group[0]?.group;
      const showGroup = ancestor && (group.length > 1 || ancestor.id !== group[0]?.issue.id);
      return <div key={ancestor?.id ?? group[0]?.issue.id} className="space-y-2">{showGroup && <h3 className="break-words text-caption font-medium"><AppLink href={paths.issueDetail(ancestor.identifier || ancestor.id)} className="rounded-sm hover:underline">{ancestor.identifier} · {ancestor.title}</AppLink></h3>}{group.map(entry => <WorkProgressEntry key={entry.issue.id} entry={entry} currentProjectId={currentProjectId} />)}</div>;
    })}
    {view && <div className="flex flex-wrap items-center justify-between gap-2 text-caption text-muted-foreground"><span>{t($ => $.work_progress.showing, { shown: entries.length, total: view.filtered_total })}</span><span>{t($ => $.work_progress.updated, { time: new Date(view.as_of).toLocaleTimeString() })}</span>{query.hasNextPage && <Button size="sm" variant="outline" disabled={query.isFetchingNextPage} aria-busy={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>{t($ => $.work_progress.load_more)}</Button>}</div>}
  </section>;
}
