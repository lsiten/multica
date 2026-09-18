"use client";
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  runtimeDisplayLabel,
  runtimeListOptions,
  VSCREEN_CAPABILITIES,
} from "@multica/core/runtimes";
import { pinListOptions, useCreatePin, useDeletePin } from "@multica/core/pins";
import { Pin, PinOff } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { PAGE_GUTTER, PAGE_RAIL } from "../../layout/page-header";
import { useT } from "../../i18n";
import {
  LegacyRuntimeMirrorPage,
  type RuntimeMirrorPageProps,
} from "./mirror/legacy-mirror-page";
import { MirrorSurface } from "./mirror/mirror-surface";
export type {
  RuntimeMirrorPageProps,
  RuntimeMirrorState,
} from "./mirror/legacy-mirror-page";

export function RuntimeMirrorPage(props: RuntimeMirrorPageProps) {
  const wsId = useWorkspaceId();
  const accountId = useAuthStore((state) => state.user?.id ?? null);
  const runtimes = useQuery(runtimeListOptions(wsId));
  const runtime = runtimes.data?.find((entry) => entry.id === props.runtimeId);
  const capabilities = runtime?.metadata.capabilities;
  const v2 =
    Array.isArray(capabilities) &&
    capabilities.includes(VSCREEN_CAPABILITIES.video) &&
    capabilities.includes(VSCREEN_CAPABILITIES.viewerGrant);
  const backendIdentity =
    api.getBaseUrl?.() ||
    (typeof window === "undefined" ? "" : window.location.origin);
  const scope = useMemo(
    () =>
      backendIdentity && accountId
        ? {
            backendIdentity: backendIdentity.replace(/\/$/, ""),
            accountId,
            workspaceId: wsId,
            runtimeId: props.runtimeId,
          }
        : undefined,
    [backendIdentity, accountId, wsId, props.runtimeId],
  );
  if (!v2 || !runtime || !accountId || props.connectionState !== undefined)
    return (
      <LegacyRuntimeMirrorPage
        key={scope ? JSON.stringify(scope) : "unresolved"}
        {...props}
        scope={scope}
      />
    );
  if (!scope) return null;
  return <VideoMirrorPage runtime={runtime} scope={scope} />;
}

function VideoMirrorPage({
  runtime,
  scope,
}: {
  readonly runtime: import("@multica/core/types").RuntimeDevice;
  readonly scope: import("@multica/core/types").VscreenScope;
}) {
  const { t } = useT("runtimes");
  const paths = useWorkspacePaths();
  const readable =
    !!runtime.owner_id &&
    (runtime.owner_id === scope.accountId || runtime.visibility === "public");
  const pins = useQuery({
    ...pinListOptions(scope.workspaceId, scope.accountId),
    enabled: readable,
  });
  const createPin = useCreatePin();
  const deletePin = useDeletePin();
  const pinned =
    pins.data?.some(
      (pin) => pin.item_type === "runtime_mirror" && pin.item_id === runtime.id,
    ) === true;
  const label = runtimeDisplayLabel(runtime);
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <BreadcrumbHeader
        segments={[{ href: paths.runtimes(), label: t(($) => $.page.title) }]}
        leaf={<span className="truncate text-foreground">{label}</span>}
        actions={
          <Button
            variant="ghost"
            size="icon-sm"
            disabled={!readable || createPin.isPending || deletePin.isPending}
            aria-label={
              pinned
                ? t(($) => $.mirror.unpin_tooltip)
                : t(($) => $.mirror.pin_tooltip)
            }
            onClick={() =>
              pinned
                ? deletePin.mutate({
                    itemType: "runtime_mirror",
                    itemId: runtime.id,
                  })
                : createPin.mutate({
                    item_type: "runtime_mirror",
                    item_id: runtime.id,
                  })
            }
          >
            {pinned ? (
              <PinOff aria-hidden="true" />
            ) : (
              <Pin aria-hidden="true" />
            )}
          </Button>
        }
      />
      <main className="min-h-0 flex-1 overflow-y-auto">
        <div className={`${PAGE_RAIL} ${PAGE_GUTTER} py-6`}>
          <div className="mx-auto max-w-5xl space-y-5">
            <div>
              <h1 className="text-title-sm font-semibold [overflow-wrap:anywhere]">{label}</h1>
              <p className="mt-1 text-body text-muted-foreground">
                {t(($) => $.mirror.description)}
              </p>
            </div>
            <MirrorSurface
              key={JSON.stringify(scope)}
              scope={scope}
              runtime={runtime}
            />
          </div>
        </div>
      </main>
    </div>
  );
}
