"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { RefreshCw, Search } from "lucide-react";
import { api } from "@multica/core/api";
import { useCurrentWorkspace } from "@multica/core/paths";
import { useCurrentMember } from "@multica/core/permissions";
import type { JevDecisionLogDetail, JevDecisionLogFilters } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Badge } from "@multica/ui/components/ui/badge";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@multica/ui/components/ui/table";
import { useLocale, useT } from "../../i18n";
import { SettingsTab } from "./settings-layout";

type LogFilters = { q: string; agent: string; source: string; result_class: string; from: string; to: string };
const emptyFilters: LogFilters = { q: "", agent: "", source: "", result_class: "", from: "", to: "" };
const selectClass = "h-9 min-w-0 rounded-md border bg-background px-2 text-caption";

function formatLogTime(raw: string, locale: string): string {
  const date = new Date(raw);
  return Number.isNaN(date.getTime()) ? raw : date.toLocaleString(locale);
}

export function JevDecisionLogsTab() {
  const { t } = useT("settings");
  const workspace = useCurrentWorkspace();
  const member = useCurrentMember(workspace?.id ?? "");
  const canRead = member.role === "owner" || member.role === "admin";
  return <SettingsTab title={t(($) => $.jev_logs.title)} scope="workspace">
    {canRead && workspace ? <JevDecisionLogs key={workspace.id} workspaceID={workspace.id} /> : <p className="text-caption text-muted-foreground">{t(($) => $.jev.admin_only)}</p>}
  </SettingsTab>;
}

function JevDecisionLogs({ workspaceID }: { workspaceID: string }) {
  const { t } = useT("settings");
  const locale = useLocale();
  const [draft, setDraft] = useState<LogFilters>(emptyFilters);
  const [filters, setFilters] = useState<LogFilters>(emptyFilters);
  const [page, setPage] = useState<{ offset: number; limit: number; generation: number; asOf?: string }>({ offset: 0, limit: 20, generation: 0 });
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [timeError, setTimeError] = useState(false);
  const params: JevDecisionLogFilters = {
    q: filters.q.trim(), agent: filters.agent.trim(), source: filters.source, result_class: filters.result_class,
    from: filters.from ? new Date(`${filters.from}T00:00:00`).toISOString() : undefined,
    to: filters.to ? new Date(`${filters.to}T23:59:59.999`).toISOString() : undefined,
    offset: page.offset, limit: page.limit, as_of: page.asOf,
  };
  const query = useQuery({ queryKey: ["jev-decision-logs", workspaceID, params, page.generation], queryFn: () => api.listJevDecisionLogs(workspaceID, params), refetchInterval: 10_000 });
  const data = query.data;
  useEffect(() => {
    if (data && page.offset > 0 && page.offset >= data.total) {
      setPage((current) => ({ ...current, offset: Math.max(0, Math.ceil(data.total / current.limit) - 1) * current.limit, asOf: data.as_of }));
    }
  }, [data, page.offset]);
  const sourceLabels: Record<string, string> = { local: t(($) => $.jev.local), remote: t(($) => $.jev.remote), agent_context: t(($) => $.jev.agent_context), system_one: t(($) => $.jev_logs.native_protocol), unknown: t(($) => $.jev_logs.unknown) };
  const resultLabels: Record<string, string> = { running: t(($) => $.jev_logs.running), success: t(($) => $.jev_logs.success), error: t(($) => $.jev_logs.error), rejected: t(($) => $.jev_logs.rejected), unknown: t(($) => $.jev_logs.unknown) };
  const apply = () => {
    if (draft.from && draft.to && draft.from > draft.to) { setTimeError(true); return; }
    setTimeError(false); setFilters(draft); setPage({ offset: 0, limit: page.limit, generation: page.generation + 1 }); setSelectedID(null);
  };
  const pages = Math.max(1, Math.ceil((data?.total ?? 0) / page.limit));
  const currentPage = Math.floor(page.offset / page.limit) + 1;
  const goToPage = (next: number) => setPage({ ...page, offset: (next - 1) * page.limit, asOf: data?.as_of ?? page.asOf });
  const updateDraft = (patch: Partial<LogFilters>) => { setDraft((current) => ({ ...current, ...patch })); setTimeError(false); };
  const refresh = () => { setPage({ offset: 0, limit: page.limit, generation: page.generation + 1 }); setSelectedID(null); };

  return <Dialog open={selectedID !== null} onOpenChange={(open) => { if (!open) setSelectedID(null); }}>
    <div className="space-y-4">
      <form onSubmit={(event) => { event.preventDefault(); apply(); }} className="space-y-3">
        <div className="flex flex-wrap gap-2">
          <label className="min-w-52 flex-1"><span className="mb-1 block text-caption">{t(($) => $.jev_logs.search)}</span><Input type="search" maxLength={500} value={draft.q} onChange={(event) => updateDraft({ q: event.target.value })} placeholder={t(($) => $.jev_logs.search_placeholder)} /></label>
          <label className="min-w-40"><span className="mb-1 block text-caption">{t(($) => $.jev_logs.agent)}</span><Input maxLength={256} value={draft.agent} onChange={(event) => updateDraft({ agent: event.target.value })} placeholder={t(($) => $.jev_logs.agent_placeholder)} /></label>
          <label><span className="mb-1 block text-caption">{t(($) => $.jev_logs.result)}</span><select className={selectClass} value={draft.result_class} onChange={(event) => updateDraft({ result_class: event.target.value })}><option value="">{t(($) => $.jev_logs.all_results)}</option>{["running", "success", "error", "rejected"].map((result) => <option key={result} value={result}>{resultLabels[result]}</option>)}</select></label>
          <label><span className="mb-1 block text-caption">{t(($) => $.jev_logs.source)}</span><select className={selectClass} value={draft.source} onChange={(event) => updateDraft({ source: event.target.value })}><option value="">{t(($) => $.jev_logs.all_sources)}</option>{["local", "agent_context", "remote", "system_one"].map((source) => <option key={source} value={source}>{sourceLabels[source]}</option>)}</select></label>
        </div>
        <div className="flex flex-wrap items-end gap-2">
          <label><span className="mb-1 block text-caption">{t(($) => $.jev_logs.from)}</span><Input type="date" value={draft.from} onChange={(event) => updateDraft({ from: event.target.value })} /></label>
          <label><span className="mb-1 block text-caption">{t(($) => $.jev_logs.to)}</span><Input type="date" min={draft.from || undefined} value={draft.to} onChange={(event) => updateDraft({ to: event.target.value })} /></label>
          <Button type="submit" size="sm" disabled={query.isFetching}><Search />{t(($) => $.jev_logs.apply)}</Button>
          <Button type="button" variant="ghost" size="sm" onClick={() => { setDraft(emptyFilters); setFilters(emptyFilters); setTimeError(false); refresh(); }}>{t(($) => $.jev_logs.clear)}</Button>
          <Button type="button" variant="outline" size="sm" className="ml-auto" disabled={query.isFetching} onClick={refresh}><RefreshCw />{t(($) => $.jev_logs.refresh)}</Button>
        </div>
        {timeError && <p role="alert" className="text-caption text-destructive">{t(($) => $.jev_logs.time_invalid)}</p>}
      </form>
      {query.isPending ? <p role="status" className="py-8 text-center text-caption text-muted-foreground">{t(($) => $.jev_logs.loading)}</p> : query.isError ? <div role="alert" className="flex items-center gap-2 text-caption text-destructive">{t(($) => $.jev_logs.load_failed)}<Button size="sm" variant="outline" onClick={() => void query.refetch()}>{t(($) => $.jev.retry)}</Button></div> : <>
        <div className="rounded-md border">
          <Table>
            <TableHeader><TableRow>{[t(($) => $.jev_logs.time), t(($) => $.jev_logs.task), t(($) => $.jev_logs.agent), t(($) => $.jev_logs.model), t(($) => $.jev_logs.result), t(($) => $.jev_logs.duration), t(($) => $.jev_logs.details)].map((heading) => <TableHead key={heading} className="text-caption">{heading}</TableHead>)}</TableRow></TableHeader>
            <TableBody>{data?.items.length ? data.items.map((log) => <TableRow key={log.id}>
              <TableCell className="whitespace-nowrap text-caption"><time dateTime={log.started_at}>{formatLogTime(log.started_at, locale)}</time></TableCell>
              <TableCell className="max-w-44 truncate text-caption" title={log.task_id}>{log.issue_identifier || log.task_id}</TableCell>
              <TableCell className="max-w-40 truncate text-caption" title={log.agent_id}>{log.agent_name || log.agent_id}</TableCell>
              <TableCell className="max-w-52 text-caption"><div className="truncate" title={log.model}>{log.model || "—"}</div><span className="text-muted-foreground">{sourceLabels[log.source] ?? t(($) => $.jev_logs.unknown)}</span></TableCell>
              <TableCell><Badge variant="outline" className={log.result_class === "error" || log.result_class === "rejected" ? "border-destructive/30 text-destructive" : ""}>{resultLabels[log.result_class] ?? t(($) => $.jev_logs.unknown)}</Badge></TableCell>
              <TableCell className="whitespace-nowrap text-caption tabular-nums">{log.result_class === "running" ? "—" : t(($) => $.jev_logs.milliseconds, { value: log.duration_ms })}</TableCell>
              <TableCell><DialogTrigger render={<Button variant="ghost" size="sm" />} aria-label={t(($) => $.jev_logs.details_for, { id: log.id })} onClick={() => setSelectedID(log.id)}>{t(($) => $.jev_logs.details)}</DialogTrigger></TableCell>
            </TableRow>) : <TableRow><TableCell colSpan={7} className="py-12 text-center text-caption text-muted-foreground">{t(($) => $.jev_logs.empty)}</TableCell></TableRow>}</TableBody>
          </Table>
        </div>
        <div className="flex flex-wrap items-center gap-3 text-caption">
          <span role="status" className="text-muted-foreground">{t(($) => $.jev_logs.total, { count: data?.total ?? 0 })}</span>
          <label className="flex items-center gap-2">{t(($) => $.jev_logs.page_size)}<select className={selectClass} value={page.limit} onChange={(event) => setPage({ ...page, offset: 0, limit: Number(event.target.value), asOf: data?.as_of })}>{[20, 50, 100].map((size) => <option key={size} value={size}>{size}</option>)}</select></label>
          <div className="ml-auto flex items-center gap-2"><Button size="sm" variant="outline" disabled={currentPage <= 1 || query.isFetching} onClick={() => goToPage(currentPage - 1)}>{t(($) => $.jev_logs.previous)}</Button><span className="tabular-nums">{t(($) => $.jev_logs.page, { page: currentPage, pages })}</span><Button size="sm" variant="outline" disabled={currentPage >= pages || query.isFetching} onClick={() => goToPage(currentPage + 1)}>{t(($) => $.jev_logs.next)}</Button></div>
        </div>
      </>}
    </div>
    <DialogContent className="max-h-[85dvh] overflow-y-auto sm:max-w-4xl" aria-describedby={undefined}>
      <DialogHeader><DialogTitle>{t(($) => $.jev_logs.detail_title)}</DialogTitle></DialogHeader>
      {selectedID && <JevDecisionDetail workspaceID={workspaceID} decisionID={selectedID} sourceLabels={sourceLabels} resultLabels={resultLabels} />}
    </DialogContent>
  </Dialog>;
}

function LogPayload({ label, value }: { label: string; value: string }) {
  let formatted = value;
  try { formatted = JSON.stringify(JSON.parse(value), null, 2); } catch { /* Provider responses may contain plain text. */ }
  return <section className="space-y-2"><h3 className="text-caption font-medium">{label}</h3><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-md border bg-muted/30 p-3 font-mono text-caption">{formatted || "—"}</pre></section>;
}

function JevDecisionDetail({ workspaceID, decisionID, sourceLabels, resultLabels }: { workspaceID: string; decisionID: string; sourceLabels: Record<string, string>; resultLabels: Record<string, string> }) {
  const { t } = useT("settings");
  const locale = useLocale();
  const query = useQuery<JevDecisionLogDetail>({ queryKey: ["jev-decision-log", workspaceID, decisionID], queryFn: () => api.getJevDecisionLog(workspaceID, decisionID), refetchInterval: (current) => current.state.data?.result_class === "running" ? 3_000 : false });
  if (query.isPending) return <p role="status">{t(($) => $.jev_logs.loading)}</p>;
  if (query.isError) return <div role="alert" className="space-y-2 text-caption text-destructive">{t(($) => $.jev_logs.load_failed)}<Button size="sm" variant="outline" onClick={() => void query.refetch()}>{t(($) => $.jev.retry)}</Button></div>;
  const log = query.data;
  const metadata: [string, string][] = [[t(($) => $.jev_logs.decision_id), log.id], [t(($) => $.jev_logs.task), log.issue_identifier || "—"], [t(($) => $.jev_logs.task_id), log.task_id], [t(($) => $.jev_logs.agent), log.agent_name || "—"], [t(($) => $.jev_logs.agent_id), log.agent_id], [t(($) => $.jev_logs.tool), log.tool], [t(($) => $.jev_logs.source), sourceLabels[log.source] ?? t(($) => $.jev_logs.unknown)], [t(($) => $.jev_logs.model), log.model || "—"], [t(($) => $.jev_logs.revision), log.model_revision || "—"], [t(($) => $.jev_logs.device), log.device || "—"], [t(($) => $.jev_logs.result), resultLabels[log.result_class] ?? t(($) => $.jev_logs.unknown)], [t(($) => $.jev_logs.time), formatLogTime(log.started_at, locale)]];
  return <div className="space-y-5">
    <dl className="grid grid-cols-1 gap-x-6 gap-y-3 text-caption sm:grid-cols-2">{metadata.map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{label}</dt><dd className="mt-1 break-all">{value}</dd></div>)}</dl>
    {log.error_code && <p role="status" className="text-caption text-destructive">{t(($) => $.jev_logs.error_code)}: {log.error_code}</p>}
    <LogPayload label={t(($) => $.jev_logs.input)} value={log.input} />
    <LogPayload label={t(($) => $.jev_logs.output)} value={log.output} />
    <section className="space-y-3"><h3 className="text-body font-medium">{t(($) => $.jev_logs.requests)}</h3>{log.requests.length ? log.requests.map((request) => <details key={request.attempt} className="rounded-md border p-3"><summary className="cursor-pointer text-caption">{t(($) => $.jev_logs.attempt, { value: request.attempt })} · {request.variant} · {request.http_status || "—"} · {t(($) => $.jev_logs.milliseconds, { value: request.duration_ms })}</summary><div className="mt-3 space-y-4">{request.response_incomplete && <p role="status" className="text-caption text-warning">{t(($) => $.jev_logs.response_incomplete)}</p>}<LogPayload label={t(($) => $.jev_logs.request_input)} value={request.input} /><LogPayload label={t(($) => $.jev_logs.request_output)} value={request.output} /></div></details>) : <p className="text-caption text-muted-foreground">{t(($) => $.jev_logs.no_requests)}</p>}</section>
  </div>;
}
