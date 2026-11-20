"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, Download, Loader2, Pause, Play, RefreshCw } from "@multica/ui/icons";
import { applicationLogsOptions, type Application, type ApplicationBoard } from "@multica/core/applications";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../i18n";
import { ApplicationSelect } from "./controls";
import { copyText } from "@multica/ui/lib/clipboard";

export function ApplicationLogs({ workspaceId, application, board }: { workspaceId: string; application: Application; board: ApplicationBoard }) {
  const { t } = useT("applications");
  const client = useQueryClient();
  const instances = board.instances.filter((instance) => instance.application_id === application.id && instance.can_manage);
  const [selected, setSelected] = useState(instances[0]?.id ?? "");
  const instanceId = instances.some((instance) => instance.id === selected) ? selected : instances[0]?.id ?? "";
  const [paused, setPaused] = useState(false);
  const [search, setSearch] = useState("");
  const [level, setLevel] = useState("all");
  const [since, setSince] = useState("");
  const [copied, setCopied] = useState(false);
  const managed = application.config.mode === "managed";
  const logs = useQuery({ ...applicationLogsOptions(workspaceId, application.id, instanceId, client), enabled: !!instanceId && managed && !paused });
  const text = logs.data?.text ?? "";
  const visibleText = text.split("\n").filter((line) => {
    if (search && !line.toLocaleLowerCase().includes(search.toLocaleLowerCase())) return false;
    if (level !== "all" && !new RegExp(`\\b${level}\\b`, "i").test(line)) return false;
    if (since) {
      const match = line.match(/(\\d{4}-\\d{2}-\\d{2}(?:[T ]\\d{2}:\\d{2}:\\d{2})?)/);
      if (match?.[1] && new Date(match[1]).getTime() < new Date(`${since}T00:00:00`).getTime()) return false;
    }
    return true;
  }).join("\n");
  const lines = visibleText.split("\n");

  function download() {
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `application-${application.id}-${instanceId}.log`;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  function copyLogs() {
    void copyText(visibleText).then((ok) => {
      if (!ok) return;
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }

  return <section className="space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <h2 className="text-body font-medium">{t(($) => $.logs)}</h2>
      <div className="flex flex-wrap items-center gap-2">
        <ApplicationSelect value={instanceId} onChange={setSelected} label={t(($) => $.instances)} items={instances.map((instance) => ({ value: instance.id, label: `${t(($) => $.revision)} ${instance.revision} · ${instance.id.slice(0, 8)}` }))} />
        <Button variant="ghost" size="icon-sm" disabled={!instanceId || !managed} aria-label={paused ? t(($) => $.resume_logs) : t(($) => $.pause_logs)} aria-pressed={paused} onClick={() => setPaused(!paused)}>{paused ? <Play /> : <Pause />}</Button>
        <Button variant="ghost" size="icon-sm" disabled={!instanceId || !managed || logs.isFetching} aria-label={t(($) => $.refresh)} onClick={() => void logs.refetch()}><RefreshCw /></Button>
        <Button variant="ghost" size="icon-sm" disabled={!visibleText} aria-label={t(($) => $.copy_logs)} onClick={copyLogs}>{copied ? <Check className="text-success" /> : <Copy />}</Button>
        <Button variant="ghost" size="icon-sm" disabled={!text} aria-label={t(($) => $.download_logs)} onClick={download}><Download /></Button>
      </div>
    </div>
    {managed && instanceId && <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_10rem_10rem]"><Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t(($) => $.search_logs)} aria-label={t(($) => $.search_logs)} /><ApplicationSelect value={level} onChange={setLevel} label={t(($) => $.log_level)} items={[{ value: "all", label: t(($) => $.all_log_levels) }, { value: "error", label: t(($) => $.log_error) }, { value: "warn", label: t(($) => $.log_warn) }, { value: "info", label: t(($) => $.log_info) }, { value: "debug", label: t(($) => $.log_debug) }]} /><label className="space-y-1 text-caption"><span>{t(($) => $.log_since)}</span><Input type="date" value={since} onChange={(event) => setSince(event.target.value)} /></label></div>}
    {logs.data?.gap && <p role="status" className="text-caption text-muted-foreground">{t(($) => $.log_gap)}</p>}
    {logs.data?.truncated && <p className="text-caption text-muted-foreground">{t(($) => $.log_truncated)}</p>}
    {!managed ? <p className="text-caption text-muted-foreground">{t(($) => $.external_help)}</p> : !instanceId ? <p className="text-caption text-muted-foreground">{t(($) => $.no_instances)}</p> : logs.isPending ? <Loader2 className="size-4 animate-spin" /> : logs.isError ? <p role="alert" className="break-words text-caption text-destructive">{logs.error.message}</p> : <pre className="max-h-[65dvh] min-h-40 overflow-auto rounded-xl border bg-muted/30 p-4 font-mono text-caption"><code>{lines.map((line, index) => <span key={`${index}-${line}`} className="block whitespace-pre-wrap break-words"><span aria-hidden className="mr-4 inline-block w-8 select-none text-right text-faint-foreground">{index + 1}</span>{line}</span>)}</code></pre>}
  </section>;
}
