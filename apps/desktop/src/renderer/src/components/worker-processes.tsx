import { useState } from "react";
import { Loader2, RefreshCw, Square } from "lucide-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { SettingsCard, SettingsSection } from "@multica/views/settings";
import { useT } from "@multica/views/i18n";
import { toast } from "sonner";
import type { DaemonStatus, DaemonWorkerProcess } from "../../../shared/daemon-types";

function formatStartedAt(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso || "—";
  return date.toLocaleString();
}

// A live worker's probe state maps to a colored dot, matching the daemon
// diagnostics row. "ready" is the healthy terminal; "unavailable" means the
// owner's health probe failed so it is suspect, not stopped.
function stateDotClass(row: DaemonWorkerProcess): string {
  if (row.ready) return "bg-emerald-500";
  if (row.state === "unavailable") return "bg-red-500";
  return "bg-amber-500";
}

// Local-only panel: it reads the daemon's /runtimes/processes endpoint and
// never reports worker state to the server.
export function WorkerProcessManager({ status }: { status: DaemonStatus }) {
  const { t } = useT("settings");
  const queryKey = ["desktop-worker-processes", status.profile, status.daemonId];
  const enabled = status.state === "running";
  const query = useQuery({
    queryKey,
    queryFn: () => window.daemonAPI.getWorkerProcesses(),
    enabled,
    retry: false,
    refetchInterval: enabled ? 30_000 : false,
  });
  const [stopTarget, setStopTarget] = useState<DaemonWorkerProcess | null>(null);
  const stop = useMutation({
    mutationFn: (execId: string) => window.daemonAPI.stopWorkerProcess(execId),
    onSuccess: () => {
      toast.success(t(($) => $.desktop.worker_processes.stopped));
      setStopTarget(null);
    },
    onError: (error) => {
      toast.error(t(($) => $.desktop.worker_processes.stop_failed), {
        description: error instanceof Error ? error.message : undefined,
      });
    },
    onSettled: () => void query.refetch(),
  });
  const rows = query.data ?? [];
  const busy = query.isPending || query.isFetching || stop.isPending;

  return (
    <SettingsSection
      title={t(($) => $.desktop.worker_processes.title)}
      description={t(($) => $.desktop.worker_processes.description)}
      action={
        <Button
          variant="outline"
          size="sm"
          onClick={() => void query.refetch()}
          disabled={!enabled || query.isFetching || stop.isPending}
        >
          <RefreshCw className={query.isFetching ? "size-3.5 animate-spin" : "size-3.5"} />
          {t(($) => $.desktop.worker_processes.refresh)}
        </Button>
      }
    >
      <SettingsCard>
        {!enabled && (
          <p className="px-4 py-3 text-body text-muted-foreground">
            {t(($) => $.desktop.worker_processes.offline)}
          </p>
        )}
        {enabled && query.isError && (
          <p role="alert" className="px-4 py-3 text-body text-destructive">
            {t(($) => $.desktop.worker_processes.load_failed)} {query.error?.message}
          </p>
        )}
        {enabled && !query.isPending && !query.isError && rows.length === 0 && (
          <p className="px-4 py-3 text-body text-muted-foreground">
            {t(($) => $.desktop.worker_processes.empty)}
          </p>
        )}
        {rows.map((row) => (
          <div key={row.exec_id} className="flex items-start gap-3 px-4 py-3">
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="text-body font-medium">{row.provider}</span>
                <span className="inline-flex items-center gap-1.5 text-caption text-muted-foreground">
                  <span className={`size-1.5 rounded-full ${stateDotClass(row)}`} />
                  {row.state}
                </span>
              </div>
              <p className="mt-1 truncate text-caption text-muted-foreground" title={row.task_id}>
                {t(($) => $.desktop.worker_processes.task)} {row.task_id}
              </p>
              <p className="text-caption text-muted-foreground">
                {t(($) => $.desktop.worker_processes.exec_id)} {row.exec_id} ·{" "}
                {t(($) => $.desktop.worker_processes.started)} {formatStartedAt(row.started_at)}
              </p>
              {row.capabilities && row.capabilities.length > 0 && (
                <p className="mt-1 flex flex-wrap gap-1">
                  {row.capabilities.map((capability) => (
                    <span
                      key={capability}
                      className="rounded-sm bg-muted px-1.5 py-0.5 text-caption font-medium text-muted-foreground"
                    >
                      {capability}
                    </span>
                  ))}
                </p>
              )}
            </div>
            <Button
              variant="destructive"
              size="sm"
              disabled={busy}
              onClick={() => setStopTarget(row)}
            >
              <Square className="size-3.5" />
              {t(($) => $.desktop.worker_processes.stop)}
            </Button>
          </div>
        ))}
      </SettingsCard>
      <Dialog
        open={stopTarget !== null}
        onOpenChange={(open) => {
          if (!open && !stop.isPending) setStopTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.desktop.worker_processes.stop_confirm_title)}</DialogTitle>
            <DialogDescription>{t(($) => $.desktop.worker_processes.stop_confirm_description)}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" disabled={stop.isPending} onClick={() => setStopTarget(null)}>
              {t(($) => $.desktop.daemon.cancel)}
            </Button>
            <Button
              variant="destructive"
              disabled={stop.isPending}
              onClick={() => {
                if (stopTarget) stop.mutate(stopTarget.exec_id);
              }}
            >
              {stop.isPending && <Loader2 className="size-3.5 animate-spin" />}
              {t(($) => $.desktop.worker_processes.stop)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </SettingsSection>
  );
}
