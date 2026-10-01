"use client";

import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { Badge } from "@multica/ui/components/ui/badge";
import { SettingsCard } from "./settings-layout";
import { useT } from "../../i18n";
type DaemonMcpReadiness = {
  name: string;
  instance_id?: string;
  workspace_id?: string;
  enabled: boolean;
  scope: string;
  ready: boolean;
  reason?: string;
  state: "not_configured" | "probing" | "ready" | "offline" | "timeout" | "protocol_error";
};

interface DaemonMcpAPI {
  getMcpReadiness?: () => Promise<DaemonMcpReadiness[]>;
}

function daemonAPI(): DaemonMcpAPI | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as unknown as { daemonAPI?: DaemonMcpAPI }).daemonAPI;
}

function StatusBadge({ state }: { state: DaemonMcpReadiness["state"] }) {
  const { t } = useT("settings");
  const label = (() => {
    switch (state) {
      case "ready": return t(($) => $.jev.ready);
      case "probing": return t(($) => $.jev.loading);
      case "not_configured": return t(($) => $.jev.not_installed);
      case "protocol_error": return t(($) => $.jev.daemon_invalid);
      default: return t(($) => $.jev.daemon_failed);
    }
  })();
  return <Badge variant={state === "ready" ? "secondary" : "outline"}>{label}</Badge>;
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
        {statuses.map((status) => (
          <li key={`${status.workspace_id ?? "daemon"}:${status.name}:${status.instance_id ?? "default"}`} className="flex items-center gap-3 p-4">
            <div className="min-w-0 flex-1">
              <p className="text-body font-medium">{status.name}</p>
              <p className="text-caption text-muted-foreground">{status.scope}{status.reason ? ` · ${status.reason}` : ""}</p>
            </div>
            <StatusBadge state={status.state} />
          </li>
        ))}
      </ul>
    </SettingsCard>
  );
}
