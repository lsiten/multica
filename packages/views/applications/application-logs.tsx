"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Loader2, Pause, Play, RefreshCw } from "lucide-react";
import { applicationLogsOptions, type Application, type ApplicationBoard } from "@multica/core/applications";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../i18n";
import { ApplicationSelect } from "./controls";

export function ApplicationLogs({ workspaceId, application, board }: { workspaceId: string; application: Application; board: ApplicationBoard }) {
  const { t } = useT("applications");
  const client = useQueryClient();
  const instances = board.instances.filter((instance) => instance.application_id === application.id && instance.can_manage);
  const [selected, setSelected] = useState(instances[0]?.id ?? "");
  const instanceId = instances.some((instance) => instance.id === selected) ? selected : instances[0]?.id ?? "";
  const [paused, setPaused] = useState(false);
  const [search, setSearch] = useState("");
  const managed = application.config.mode === "managed";
  const logs = useQuery({ ...applicationLogsOptions(workspaceId, application.id, instanceId, client), enabled: !!instanceId && managed && !paused });
  const text = logs.data?.text ?? "";
  const visibleText = search ? text.split("\n").filter((line) => line.toLocaleLowerCase().includes(search.toLocaleLowerCase())).join("\n") : text;

  function download() {
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `application-${application.id}-${instanceId}.log`;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  return <section className="space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <h2 className="text-body font-medium">{t(($) => $.logs)}</h2>
      <div className="flex flex-wrap items-center gap-2">
        <ApplicationSelect value={instanceId} onChange={setSelected} label={t(($) => $.instances)} items={instances.map((instance) => ({ value: instance.id, label: `${t(($) => $.revision)} ${instance.revision} · ${instance.id.slice(0, 8)}` }))} />
        <Button variant="ghost" size="icon-sm" disabled={!instanceId || !managed} aria-label={paused ? t(($) => $.resume_logs) : t(($) => $.pause_logs)} aria-pressed={paused} onClick={() => setPaused(!paused)}>{paused ? <Play /> : <Pause />}</Button>
        <Button variant="ghost" size="icon-sm" disabled={!instanceId || !managed || logs.isFetching} aria-label={t(($) => $.refresh)} onClick={() => void logs.refetch()}><RefreshCw /></Button>
        <Button variant="ghost" size="icon-sm" disabled={!text} aria-label={t(($) => $.download_logs)} onClick={download}><Download /></Button>
      </div>
    </div>
    {managed && instanceId && <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t(($) => $.search_logs)} aria-label={t(($) => $.search_logs)} />}
    {logs.data?.gap && <p role="status" className="text-caption text-muted-foreground">{t(($) => $.log_gap)}</p>}
    {logs.data?.truncated && <p className="text-caption text-muted-foreground">{t(($) => $.log_truncated)}</p>}
    {!managed ? <p className="text-caption text-muted-foreground">{t(($) => $.external_help)}</p> : !instanceId ? <p className="text-caption text-muted-foreground">{t(($) => $.no_instances)}</p> : logs.isPending ? <Loader2 className="size-4 animate-spin" /> : logs.isError ? <p role="alert" className="break-words text-caption text-destructive">{logs.error.message}</p> : <pre className="max-h-[65dvh] min-h-40 overflow-auto whitespace-pre-wrap break-words rounded-xl border bg-muted/30 p-4 font-mono text-caption">{visibleText}</pre>}
  </section>;
}
