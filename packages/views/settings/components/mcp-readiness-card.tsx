"use client";

import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { McpReadinessBadge } from "./mcp-readiness-status";
import { SettingsCard } from "./settings-layout";
import { useT } from "../../i18n";
import { BuiltinMcpDetailsButton } from "./builtin-mcp-details";
type DaemonMcpReadiness = {
  name: string;
  instance_id?: string;
  workspace_id?: string;
  enabled: boolean;
  scope: string;
  ready: boolean;
  reason?: string;
  state: "not_configured" | "broker_ready" | "probing" | "ready" | "provider_unavailable" | "capability_required" | "offline" | "timeout" | "protocol_error";
};

interface DaemonMcpAPI {
  getMcpReadiness?: () => Promise<DaemonMcpReadiness[]>;
}

function isBuiltin(name: string) {
  return name === "multica-llm2jev" || name === "multica-identity-actions";
}

function readinessRows(statuses: DaemonMcpReadiness[]) {
  const rows: { status: DaemonMcpReadiness; connections: DaemonMcpReadiness[] }[] = [];
  const builtins = new Map<string, (typeof rows)[number]>();
  for (const status of statuses) {
    if (!isBuiltin(status.name)) {
      rows.push({ status, connections: [] });
      continue;
    }
    let row = builtins.get(status.name);
    if (!row) {
      row = { status, connections: [] };
      builtins.set(status.name, row);
      rows.push(row);
    } else if (status.scope === "daemon") {
      row.status = status;
    }
    if (status.scope === "task" && status.reason !== "task_not_started") row.connections.push(status);
  }
  return rows;
}

function daemonAPI(): DaemonMcpAPI | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as unknown as { daemonAPI?: DaemonMcpAPI }).daemonAPI;
}


/**
 * Shows readiness returned by the host daemon's bounded MCP probe. The web
 * client deliberately renders a neutral unavailable state; only Electron has
 * a profile-authenticated local daemon API.
 */
export function McpReadinessCard() {
  const { t } = useT("settings");
  const [statuses, setStatuses] = useState<DaemonMcpReadiness[] | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const api = daemonAPI();
    if (!api?.getMcpReadiness) {
      setStatuses([]);
      return;
    }
    let cancelled = false;
    const refresh = () => {
      void api.getMcpReadiness!().then((next) => {
        if (!cancelled) {
          setStatuses(next);
          setFailed(false);
        }
      }).catch(() => {
        if (!cancelled) {
          setFailed(true);
          setStatuses([]);
        }
      });
    };
    refresh();
    const timer = window.setInterval(refresh, 5_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  if (statuses === null) {
    return (
      <SettingsCard>
        <div className="flex items-center gap-2 p-4 text-caption text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          {t(($) => $.jev.loading)}
        </div>
      </SettingsCard>
    );
  }

  if (failed || statuses.length === 0) {
    return (
      <SettingsCard>
        <p className="p-4 text-caption text-muted-foreground">
          {t(($) => $.jev.daemon_unavailable)}
        </p>
      </SettingsCard>
    );
  }

  return (
    <SettingsCard>
      <ul className="divide-y divide-surface-border">
        {readinessRows(statuses).map(({ status, connections }) => {
          const failed = connections.filter((connection) => !connection.ready && connection.state !== "probing");
          const pending = connections.filter((connection) => connection.state === "probing");
          return <li key={`${status.workspace_id ?? "daemon"}:${status.name}:${isBuiltin(status.name) ? "builtin" : status.instance_id ?? "default"}`} className="flex flex-wrap items-start justify-between gap-3 p-4">
            <div className="min-w-0 flex-1 basis-64">
              <p className="break-all text-body font-medium">{status.name}</p>
              <p className="text-caption text-muted-foreground">
                {status.state === "not_configured" && status.reason === "task_not_started"
                  ? t(($) => $.jev.task_not_started)
                  : status.state === "broker_ready"
                    ? t(($) => $.jev.broker_ready_detail)
                    : `${status.scope}${status.reason ? ` · ${status.reason}` : ""}`}
              </p>
              {connections.length > 0 && <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-muted-foreground">
                <span>{t(($) => $.jev.active_run_connections, { value: connections.length })}</span>
                {failed.length > 0 && <span className="text-destructive">{t(($) => $.jev.failed_connections, { value: failed.length })}</span>}
                {pending.length > 0 && <span>{t(($) => $.jev.pending_connections, { value: pending.length })}</span>}
              </div>}
            </div>
            <div className="flex flex-wrap items-center gap-2">{isBuiltin(status.name)&&<BuiltinMcpDetailsButton name={status.name}/>}<McpReadinessBadge state={status.state} /></div>
          </li>;
        })}
      </ul>
    </SettingsCard>
  );
}
