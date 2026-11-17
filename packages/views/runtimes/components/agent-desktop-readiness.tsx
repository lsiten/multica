"use client";

import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, ShieldCheck } from "@multica/ui/icons";
import { api, getApi } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import {
  agentDesktopReadinessOptions,
  cachedAgentDesktopReadiness,
  vscreenKeys,
} from "@multica/core/runtimes";
import type {
  AgentDesktopPermissionState,
  AgentRuntime,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { toast } from "sonner";
import { ObjectPanelSection, ObjectProperty } from "../../layout/object-page";
import { useT } from "../../i18n";

export function AgentDesktopReadinessSection({
  canRepair,
  runtime,
}: {
  readonly canRepair: boolean;
  readonly runtime: AgentRuntime;
}) {
  const { t } = useT("runtimes");
  const queryClient = useQueryClient();
  const accountId = useAuthStore((state) => state.user?.id ?? null);
  const backendIdentity =
    api.getBaseUrl?.() ||
    (typeof window === "undefined" ? "" : window.location.origin);
  const scope = useMemo(
    () =>
      accountId && backendIdentity
        ? {
            accountId,
            backendIdentity: backendIdentity.replace(/\/$/, ""),
            runtimeId: runtime.id,
            workspaceId: runtime.workspace_id,
          }
        : undefined,
    [accountId, backendIdentity, runtime.id, runtime.workspace_id],
  );
  const query = useQuery({
    ...agentDesktopReadinessOptions(
      scope ?? {
        accountId: "",
        backendIdentity: "",
        runtimeId: runtime.id,
        workspaceId: runtime.workspace_id,
      },
    ),
    enabled: scope !== undefined && runtime.status === "online",
  });
  const readiness =
    query.data ?? cachedAgentDesktopReadiness(runtime.metadata);
  const repair = useMutation({
    mutationFn: () => {
      if (!scope) throw new Error("runtime scope is unavailable");
      return getApi().vscreen(scope).openAgentDesktopPermissionGuide();
    },
    onSuccess: (updated) => {
      if (scope && updated) {
        queryClient.setQueryData(vscreenKeys.agentDesktop(scope), updated);
      }
      toast.success(t(($) => $.detail.desktop.repair_started));
    },
    onError: () => {
      toast.error(t(($) => $.detail.desktop.repair_failed));
    },
  });

  if (runtime.runtime_mode !== "local") return null;
  const recheck = <div className="px-3 pb-2"><Button type="button" size="xs" variant="outline" disabled={runtime.status !== "online" || query.isFetching} aria-busy={query.isFetching} onClick={() => { void query.refetch(); }}>
    <RefreshCw aria-hidden="true" className={query.isFetching ? "animate-spin" : undefined} />{t(($) => $.detail.desktop.recheck)}
  </Button></div>;
  if (!readiness) return <ObjectPanelSection title={t(($) => $.detail.desktop.title)}>
    <p className="px-3 pb-2 text-caption text-muted-foreground" role={query.isError ? "alert" : "status"}>
      {runtime.status !== "online" ? t(($) => $.detail.desktop.offline) : query.isFetching ? t(($) => $.detail.desktop.checking) : query.isError ? t(($) => $.detail.desktop.reason.status_failed) : t(($) => $.detail.desktop.unchecked)}
    </p>
    {recheck}
  </ObjectPanelSection>;
  const reason = readiness.reason
    ? desktopReason(t, readiness.reason)
    : t(($) => $.detail.desktop.ready);

  return (
    <ObjectPanelSection title={t(($) => $.detail.desktop.title)}>
      {runtime.status !== "online" && <p role="status" className="px-3 pb-2 text-caption text-muted-foreground">{t(($) => $.detail.desktop.offline)}</p>}
      {query.isError && <p role="alert" className="px-3 pb-2 text-caption text-destructive">{t(($) => $.detail.desktop.reason.status_failed)}</p>}
      <ObjectProperty label={t(($) => $.detail.desktop.version)}>
        <ShieldCheck aria-hidden="true" className="size-3.5 shrink-0" />
        <span className="truncate font-mono">
          {t(($) => $.detail.desktop.version_value, {
            version: readiness.version || "—",
          })}
        </span>
      </ObjectProperty>
      <ObjectProperty label={t(($) => $.detail.desktop.platform)}>
        <span className="truncate capitalize">{readiness.platform || "—"}</span>
      </ObjectProperty>
      <ObjectProperty label={t(($) => $.detail.desktop.accessibility)}>
        <PermissionValue state={readiness.permissions.accessibility} />
      </ObjectProperty>
      <ObjectProperty label={t(($) => $.detail.desktop.screen_recording)}>
        <PermissionValue state={readiness.permissions.screenRecording} />
      </ObjectProperty>
      <ObjectProperty label={t(($) => $.detail.desktop.automation)}>
        <PermissionValue state={readiness.permissions.automation} />
      </ObjectProperty>
      <ObjectProperty label={t(($) => $.detail.desktop.surfaces)}>
        <span>{readiness.surfaceCount}</span>
      </ObjectProperty>
      {!readiness.ready && (
        <p className="px-3 pb-2 text-caption text-muted-foreground" role="status">
          {reason}
        </p>
      )}
      {!readiness.ready && canRepair && (
        <div className="px-3 pb-2">
          <Button
            type="button"
            size="xs"
            variant="outline"
            disabled={repair.isPending || !scope}
            aria-busy={repair.isPending}
            onClick={() => repair.mutate()}
          >
            <RefreshCw
              aria-hidden="true"
              className={repair.isPending ? "animate-spin" : undefined}
            />
            {t(($) => $.detail.desktop.repair)}
          </Button>
        </div>
      )}
      {recheck}
    </ObjectPanelSection>
  );
}

function PermissionValue({
  state,
}: {
  readonly state: AgentDesktopPermissionState;
}) {
  const { t } = useT("runtimes");
  switch (state) {
    case "granted":
      return <span>{t(($) => $.detail.desktop.permission.granted)}</span>;
    case "denied":
      return (
        <span className="text-destructive">
          {t(($) => $.detail.desktop.permission.denied)}
        </span>
      );
    case "not_required":
      return <span>{t(($) => $.detail.desktop.permission.not_required)}</span>;
    case "not_determined":
      return (
        <span>{t(($) => $.detail.desktop.permission.not_determined)}</span>
      );
    case "unknown":
      return <span>{t(($) => $.detail.desktop.permission.unknown)}</span>;
  }
}

function desktopReason(
  t: ReturnType<typeof useT<"runtimes">>["t"],
  reason: string,
) {
  switch (reason) {
    case "missing":
      return t(($) => $.detail.desktop.reason.missing);
    case "version_mismatch":
      return t(($) => $.detail.desktop.reason.version_mismatch);
    case "invalid_install":
      return t(($) => $.detail.desktop.reason.invalid_install);
    case "status_failed":
      return t(($) => $.detail.desktop.reason.status_failed);
    case "invalid_response":
      return t(($) => $.detail.desktop.reason.invalid_response);
    case "no_graphical_surface":
      return t(($) => $.detail.desktop.reason.no_graphical_surface);
    case "accessibility_denied":
      return t(($) => $.detail.desktop.reason.accessibility_denied);
    case "permissions_check_failed":
      return t(($) => $.detail.desktop.reason.permissions_check_failed);
    case "guide_failed":
      return t(($) => $.detail.desktop.reason.guide_failed);
    default:
      return t(($) => $.detail.desktop.reason.unavailable, { reason });
  }
}
