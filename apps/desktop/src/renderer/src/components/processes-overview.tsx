import { useRef, useState } from "react";
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
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@multica/ui/components/ui/tabs";
import { Switch } from "@multica/ui/components/ui/switch";
import { SettingsCard, SettingsSection } from "@multica/views/settings";
import { useT } from "@multica/views/i18n";
import { toast } from "sonner";
import type { DaemonStatus } from "../../../shared/daemon-types";

// Unified process overview: the daemon supervises several long-lived child
// processes (application, gateway, environment, mirror) plus per-execution task
// workers, and in-process services (MCP / JEV). This panel aggregates every
// category the daemon exposes locally into one manageable list, with a per-row
// state dot and a stop action for the categories that support it. Child
// processes carry best-effort CPU/memory usage; in-process services (MCP / JEV)
// are status-only and their resource usage is not metered.

type ProcessCategory =
  | "daemon"
  | "task_worker"
  | "application"
  | "gateway"
  | "environment"
  | "mirror"
  | "mcp"
  | "jev";

type UnifiedProcess = {
  category: ProcessCategory;
  id: string;
  name: string;
  state: string;
  ready: boolean;
  detail?: string;
  sub?: string;
  capabilities?: string[];
  stoppable: boolean;
  pid?: number;
  memoryBytes?: number;
  cpuTimeMS?: number;
};

function formatBytes(bytes: number | undefined): string {
  if (bytes === undefined || bytes <= 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(value < 10 && unit > 0 ? 1 : 0)} ${units[unit]}`;
}

function formatCpu(ms: number | undefined): string {
  if (ms === undefined || ms <= 0) return "—";
  if (ms < 60_000) return `${Math.round(ms)} ms`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m ${seconds}s`;
}

function dotClass(ready: boolean, state: string): string {
  if (ready) return "bg-emerald-500";
  if (state === "unavailable" || state === "offline" || state === "failed") {
    return "bg-red-500";
  }
  return "bg-amber-500";
}

// The daemon's own process is first; the rest follow the order the overview
// endpoint returns them so a category header never reorders its own rows.
const KNOWN_CATEGORIES: ProcessCategory[] = [
  "daemon",
  "task_worker",
  "application",
  "gateway",
  "environment",
  "mirror",
  "mcp",
  "jev",
];

// Child-process rows come from the daemon's unified /runtimes/processes/overview
// endpoint. The category is validated against the known set; unknown categories
// are dropped so a future backend category can never render a missing badge.
function normalizeOverview(raw: unknown): UnifiedProcess[] {
  const entries = Array.isArray(raw) ? raw : [];
  const out: UnifiedProcess[] = [];
  for (const entry of entries) {
    if (!entry || typeof entry !== "object") continue;
    const item = entry as Record<string, unknown>;
    const category = item.category as string;
    if (!KNOWN_CATEGORIES.includes(category as ProcessCategory)) continue;
    out.push({
      category: category as ProcessCategory,
      id: typeof item.id === "string" ? item.id : "",
      name: typeof item.name === "string" ? item.name : "",
      state: typeof item.state === "string" ? item.state : "unavailable",
      ready: item.ready === true,
      detail: typeof item.detail === "string" ? item.detail : undefined,
      sub: Array.isArray(item.capabilities)
        ? item.capabilities
            .filter((c): c is string => typeof c === "string")
            .join(" · ") || undefined
        : undefined,
      capabilities:
        Array.isArray(item.capabilities) &&
        item.capabilities.every((c) => typeof c === "string")
          ? (item.capabilities as string[])
          : undefined,
      stoppable: item.stoppable === true,
      pid: typeof item.pid === "number" ? item.pid : undefined,
      memoryBytes: typeof item.memory_bytes === "number" ? item.memory_bytes : undefined,
      cpuTimeMS: typeof item.cpu_time_ms === "number" ? item.cpu_time_ms : undefined,
    });
  }
  return out;
}

function normalizeMcp(raw: unknown): UnifiedProcess[] {
  const data = raw as
    | { instances?: unknown[]; services?: unknown[] }
    | undefined;
  const out: UnifiedProcess[] = [];
  const seen = new Set<string>();
  for (const inst of data?.instances ?? []) {
    const item = inst as {
      name?: string;
      state?: string;
      ready?: boolean;
      enabled?: boolean;
      reason?: string;
    };
    if (typeof item.name !== "string") continue;
    if (seen.has(item.name)) continue;
    seen.add(item.name);
    out.push({
      category: "mcp",
      id: item.name,
      name: item.name,
      state: item.state ?? (item.ready ? "ready" : "not_ready"),
      ready: item.ready === true,
      detail: item.enabled === false ? "disabled" : undefined,
      sub: item.reason,
      stoppable: false,
    });
  }
  for (const svc of data?.services ?? []) {
    const item = svc as { name?: string };
    if (typeof item.name !== "string" || seen.has(item.name)) continue;
    seen.add(item.name);
    out.push({
      category: "mcp",
      id: item.name,
      name: item.name,
      state: "ready",
      ready: true,
      stoppable: false,
    });
  }
  return out;
}

function normalizeJev(raw: unknown): UnifiedProcess[] {
  const data = raw as
    | { models?: unknown[]; status?: unknown[] }
    | undefined;
  const out: UnifiedProcess[] = [];
  for (const entry of data?.status ?? []) {
    const item = entry as {
      model_id?: string;
      state?: string;
      installed?: boolean;
      downloaded_bytes?: number;
      total_bytes?: number;
    };
    if (typeof item.model_id !== "string") continue;
    const installed = item.installed === true;
    let detail: string | undefined;
    if (!installed && typeof item.total_bytes === "number" && item.total_bytes > 0) {
      const pct = item.downloaded_bytes ? Math.round((item.downloaded_bytes / item.total_bytes) * 100) : 0;
      detail = `${pct}%`;
    }
    out.push({
      category: "jev",
      id: item.model_id,
      name: item.model_id,
      state: item.state ?? (installed ? "installed" : "not_installed"),
      ready: installed,
      detail,
      stoppable: false,
    });
  }
  return out;
}

export function ProcessesOverview({ status }: { status: DaemonStatus }) {
  const { t } = useT("settings");
  const enabled = status.state === "running";
  const queryKey = ["desktop-process-overview", status.profile, status.daemonId];
  const overview = useQuery({
    queryKey: [...queryKey, "overview"],
    queryFn: () => window.daemonAPI.getProcessOverview(),
    enabled,
    retry: false,
    refetchInterval: enabled ? 10_000 : false,
  });
  const mcp = useQuery({
    queryKey: [...queryKey, "mcp"],
    queryFn: () => window.daemonAPI.getBuiltinMcpServices(),
    enabled,
    retry: false,
    refetchInterval: enabled ? 10_000 : false,
  });
  const jev = useQuery({
    queryKey: [...queryKey, "jev"],
    queryFn: () => window.daemonAPI.getJevModels(),
    enabled,
    retry: false,
    refetchInterval: enabled ? 10_000 : false,
  });

  const [stopTarget, setStopTarget] = useState<UnifiedProcess | null>(null);
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
  });

  const rows = [
    ...normalizeOverview(overview.data),
    ...normalizeMcp(mcp.data),
    ...normalizeJev(jev.data),
  ];
  // Group rows by category so the panel shows one section per supervised
  // category instead of a flat list. A category with no rows is not rendered;
  // the overview always emits the daemon + service categories, so only MCP/JEV
  // are skipped when a daemon exposes none of them.
  const grouped = new Map<ProcessCategory, UnifiedProcess[]>();
  for (const row of rows) {
    const bucket = grouped.get(row.category);
    if (bucket) bucket.push(row);
    else grouped.set(row.category, [row]);
  }
  const busy =
    overview.isPending || mcp.isPending || jev.isPending || stop.isPending;
  const failed =
    (overview.isError && !mcp.data && !jev.data) ||
    (mcp.isError && !overview.data && !jev.data);

  // One row, rendered in both the "All" tab and each category tab. Resource
  // usage is always shown — formatBytes/formatCpu degrade to "—" when a
  // reading is unavailable, so the panel never hides the resource column.
  const renderRow = (row: UnifiedProcess) => (
    <div key={`${row.category}:${row.id}`} className="flex items-start gap-3 px-4 py-3">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-body font-medium">{row.name}</span>
          <span className="inline-flex items-center gap-1.5 text-caption text-muted-foreground">
            <span className={`size-1.5 rounded-full ${dotClass(row.ready, row.state)}`} />
            {row.state}
          </span>
        </div>
        {row.detail && (
          <p className="mt-1 truncate text-caption text-muted-foreground" title={row.detail}>
            {row.detail}
          </p>
        )}
        {row.sub && (
          <p className="mt-1 flex flex-wrap gap-1">
            {row.sub.split(" · ").map((cap) => (
              <span
                key={cap}
                className="rounded-sm bg-muted px-1.5 py-0.5 text-caption font-medium text-muted-foreground"
              >
                {cap}
              </span>
            ))}
          </p>
        )}
        <p className="mt-1 text-caption text-muted-foreground">
          {t(($) => $.desktop.processes.memory)} {formatBytes(row.memoryBytes)} ·{" "}
          {t(($) => $.desktop.processes.cpu)} {formatCpu(row.cpuTimeMS)}
        </p>
      </div>
      {row.stoppable && (
        <Button
          variant="destructive"
          size="sm"
          disabled={busy}
          onClick={() => setStopTarget(row)}
        >
          <Square className="size-3.5" />
          {t(($) => $.desktop.processes.stop)}
        </Button>
      )}
    </div>
  );

  return (
    <>
    <SettingsSection
      title={t(($) => $.desktop.processes.title)}
      description={t(($) => $.desktop.processes.description)}
      action={
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            void overview.refetch();
            void mcp.refetch();
            void jev.refetch();
          }}
          disabled={!enabled || busy}
        >
          <RefreshCw className={busy ? "size-3.5 animate-spin" : "size-3.5"} />
          {t(($) => $.desktop.processes.refresh)}
        </Button>
      }
    >
      <SettingsCard>
        {!enabled && (
          <p className="px-4 py-3 text-body text-muted-foreground">
            {t(($) => $.desktop.processes.offline)}
          </p>
        )}
        {enabled && failed && (
          <p role="alert" className="px-4 py-3 text-body text-destructive">
            {t(($) => $.desktop.processes.load_failed)}
          </p>
        )}
        {enabled && !failed && rows.length === 0 && (
          <p className="px-4 py-3 text-body text-muted-foreground">
            {t(($) => $.desktop.processes.empty)}
          </p>
        )}
        {enabled && !failed && rows.length > 0 && (
          <div className="flex flex-col">
            <ResourceUsageChart rows={rows} />
          <Tabs defaultValue="all" className="w-full">
            <TabsList variant="line" className="border-b border-border/60 px-3">
              <TabsTrigger value="all">
                {t(($) => $.desktop.processes.all)}
              </TabsTrigger>
              {KNOWN_CATEGORIES.filter((category) => grouped.has(category)).map((category) => (
                <TabsTrigger value={category} key={category}>
                  {t(($) => $.desktop.processes.category[category])}
                </TabsTrigger>
              ))}
            </TabsList>
            <TabsContent value="all" className="px-0 pb-2">
              {KNOWN_CATEGORIES.filter((category) => grouped.has(category)).map((category) => (
                <div key={category} className="border-t border-border/60 first:border-t-0">
                  <p className="px-4 pt-4 pb-1 text-caption font-medium uppercase tracking-wide text-muted-foreground">
                    {t(($) => $.desktop.processes.category[category])}
                  </p>
                  {grouped.get(category)!.map(renderRow)}
                </div>
              ))}
            </TabsContent>
            {KNOWN_CATEGORIES.filter((category) => grouped.has(category)).map((category) => (
              <TabsContent value={category} key={category} className="px-0 pb-2">
                {grouped.get(category)!.map(renderRow)}
              </TabsContent>
            ))}
          </Tabs>
          </div>
        )}
      </SettingsCard>
      <Dialog
        open={stopTarget !== null}
        onOpenChange={(open) => {
          if (!open && !stop.isPending) setStopTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.desktop.processes.stop_confirm_title)}</DialogTitle>
            <DialogDescription>{t(($) => $.desktop.processes.stop_confirm_description)}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" disabled={stop.isPending} onClick={() => setStopTarget(null)}>
              {t(($) => $.desktop.daemon.cancel)}
            </Button>
            <Button
              variant="destructive"
              disabled={stop.isPending}
              onClick={() => {
                if (stopTarget) stop.mutate(stopTarget.id);
              }}
            >
              {stop.isPending && <Loader2 className="size-3.5 animate-spin" />}
              {t(($) => $.desktop.processes.stop)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </SettingsSection>
      <ProcessServicesConfig status={status} />
    </>
  );
}

// Overall resource-usage chart: a compact summary (total memory / total CPU
// time across every row) plus a per-category memory bar chart. Memory is the
// live occupancy so it drives the bar widths; CPU time is cumulative and is
// shown only as a value. A category with no readable memory contributes 0, and
// the whole chart is hidden when nothing can be sampled (all readings "—").
function ResourceUsageChart({ rows }: { rows: UnifiedProcess[] }) {
  const { t } = useT("settings");
  const byCategory = new Map<ProcessCategory, { memory: number; cpu: number }>();
  for (const row of rows) {
    const agg = byCategory.get(row.category) ?? { memory: 0, cpu: 0 };
    agg.memory += row.memoryBytes ?? 0;
    agg.cpu += row.cpuTimeMS ?? 0;
    byCategory.set(row.category, agg);
  }
  const bars = [...byCategory.entries()]
    .map(([category, agg]) => ({ category, ...agg }))
    .sort((a, b) => b.memory - a.memory);
  const totalMemory = bars.reduce((sum, bar) => sum + bar.memory, 0);
  const totalCpu = bars.reduce((sum, bar) => sum + bar.cpu, 0);
  const maxMemory = bars.reduce((max, bar) => Math.max(max, bar.memory), 0);
  if (totalMemory <= 0) return null;
  return (
    <div className="px-4 pt-3 pb-1">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-caption font-medium uppercase tracking-wide text-muted-foreground">
          {t(($) => $.desktop.processes.usage.title)}
        </span>
        <span className="text-caption text-muted-foreground">
          {t(($) => $.desktop.processes.usage.total_memory)} {formatBytes(totalMemory)} ·{" "}
          {t(($) => $.desktop.processes.usage.total_cpu)} {formatCpu(totalCpu)}
        </span>
      </div>
      <div className="mt-3 flex flex-col gap-2">
        {bars.map((bar) => (
          <div key={bar.category} className="flex items-center gap-3">
            <span className="w-16 shrink-0 truncate text-caption text-muted-foreground">
              {t(($) => $.desktop.processes.category[bar.category])}
            </span>
            <div className="relative h-2 flex-1 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary/80"
                style={{ width: `${maxMemory > 0 ? (bar.memory / maxMemory) * 100 : 0}%` }}
              />
            </div>
            <span className="w-16 shrink-0 text-right text-caption text-muted-foreground">
              {formatBytes(bar.memory)}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

// The profile's opt-in process ownership roles. Toggling one rewrites the
// profile config (multica config set process_services) and restarts the daemon,
// so a change is never applied silently — it always goes through the confirm
// dialog below, since a restart interrupts in-flight task work.
const SUPPORTED_PROCESS_SERVICES = [
  "ai",
  "mirror",
  "application",
  "environment",
  "gateway",
] as const;
type ProcessService = (typeof SUPPORTED_PROCESS_SERVICES)[number];

// Hard safety deadline for the apply-and-restart IPC. The request is bounded by
// the config-write and stop/start child timeouts and now runs through the
// lifecycle gate, but a stalled IPC must never trap the confirm dialog: after
// this deadline the applying flag is cleared so the dialog can still be closed.
const APPLY_HARD_TIMEOUT_MS = 180_000;

function ProcessServicesConfig({ status }: { status: DaemonStatus }) {
  const { t } = useT("settings");
  const current = status.processServices ?? [];
  const enabled = new Set(current);
  const [pending, setPending] = useState<ProcessService | null>(null);
  const [applying, setApplying] = useState(false);
  // Monotonic per-apply generation: a result (or the hard timeout) only acts
  // while it still owns the current generation, so a superseded apply can never
  // clear a newer dialog or surface a stale toast.
  const applyGenRef = useRef(0);

  const nextSet = (service: string): string[] =>
    enabled.has(service)
      ? current.filter((item) => item !== service)
      : [...current, service];

  const confirm = () => {
    if (!pending) return;
    const service = pending;
    const gen = ++applyGenRef.current;
    const isCurrent = () => applyGenRef.current === gen;
    setApplying(true);
    let settled = false;
    const clear = () => {
      if (!isCurrent()) return;
      setApplying(false);
      setPending(null);
    };
    const timeout = setTimeout(() => {
      if (settled) return;
      settled = true;
      clear();
    }, APPLY_HARD_TIMEOUT_MS);
    window.daemonAPI
      .setProcessServices(nextSet(service))
      .then((result) => {
        if (settled || !isCurrent()) return;
        settled = true;
        clearTimeout(timeout);
        if (result.success) {
          toast.success(t(($) => $.desktop.processes.service_config.applied));
        } else {
          toast.error(t(($) => $.desktop.processes.service_config.apply_failed), {
            description: result.error,
          });
        }
      })
      .catch((error) => {
        if (settled || !isCurrent()) return;
        settled = true;
        clearTimeout(timeout);
        toast.error(t(($) => $.desktop.processes.service_config.apply_failed), {
          description: error instanceof Error ? error.message : undefined,
        });
      })
      .finally(() => {
        clearTimeout(timeout);
        clear();
      });
  };

  return (
    <SettingsSection
      title={t(($) => $.desktop.processes.service_config.title)}
      description={t(($) => $.desktop.processes.service_config.description)}
    >
      <SettingsCard>
        {SUPPORTED_PROCESS_SERVICES.map((service) => (
          <div key={service} className="flex items-center gap-3 px-4 py-3">
            <div className="min-w-0 flex-1">
              <span className="text-body font-medium">
                {t(($) => $.desktop.processes.service[service])}
              </span>
            </div>
            <Switch
              size="sm"
              checked={enabled.has(service)}
              disabled={applying}
              onCheckedChange={() => setPending(service)}
            />
          </div>
        ))}
      </SettingsCard>
      <Dialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open && !applying) setPending(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t(($) => $.desktop.processes.service_config.restart_confirm_title)}
            </DialogTitle>
            <DialogDescription>
              {t(($) => $.desktop.processes.service_config.restart_confirm_description)}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" disabled={applying} onClick={() => setPending(null)}>
              {t(($) => $.desktop.daemon.cancel)}
            </Button>
            <Button disabled={applying} onClick={confirm}>
              {applying && <Loader2 className="size-3.5 animate-spin" />}
              {t(($) => $.desktop.processes.service_config.apply)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </SettingsSection>
  );
}
