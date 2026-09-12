"use client";

import { useState } from "react";
import { Monitor, Pin, PinOff, RefreshCw, Wifi, WifiOff } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  isRuntimeUsableForUser,
  runtimeDisplayLabel,
  runtimeListOptions,
  runtimeMirrorAvailability,
} from "@multica/core/runtimes";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { pinListOptions, useCreatePin, useDeletePin } from "@multica/core/pins";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { AppLink } from "../../navigation";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { PAGE_GUTTER, PAGE_RAIL } from "../../layout/page-header";
import { useT } from "../../i18n";
import {
  isTerminalMirrorState,
  type RuntimeMirrorPageState,
  runtimeMirrorPageState,
} from "./mirror/mirror-page-state";
import { mirrorStateCopy } from "./mirror/mirror-state-copy";
import { useRuntimeMirrorSession } from "./mirror/use-runtime-mirror-session";

export type RuntimeMirrorState = RuntimeMirrorPageState;

export interface RuntimeMirrorPageProps {
  readonly runtimeId: string;
  readonly connectionState?: RuntimeMirrorState;
}

export function RuntimeMirrorPage({
  runtimeId,
  connectionState,
}: RuntimeMirrorPageProps) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const currentUserId = useAuthStore((state) => state.user?.id ?? null);
  const runtimeQuery = useQuery(runtimeListOptions(wsId));
  const runtime = runtimeQuery.data?.find((candidate) => candidate.id === runtimeId);
  const canReadRuntime = runtime
    ? isRuntimeUsableForUser(runtime, currentUserId)
    : false;
  const pinsQuery = useQuery({
    ...pinListOptions(wsId, currentUserId ?? ""),
    enabled: canReadRuntime && !!currentUserId,
  });
  const createPin = useCreatePin();
  const deletePin = useDeletePin();
  const availability = runtime ? runtimeMirrorAvailability(runtime) : null;
  const [retryNonce, setRetryNonce] = useState(0);
  const mirror = useRuntimeMirrorSession({
    runtimeId,
    enabled:
      connectionState === undefined &&
      runtimeQuery.isSuccess &&
      !!runtime &&
      canReadRuntime &&
      availability?.enabled === true,
    retryNonce,
  });

  const state = connectionState ?? runtimeMirrorPageState({
    queryPending: runtimeQuery.isPending,
    queryError: !!runtimeQuery.error,
    runtimeFound: !!runtime,
    canReadRuntime,
    availability: availability?.kind ?? null,
    transportState: mirror.state,
    failureReason: mirror.failureReason,
  });
  const runtimeName = runtime
    ? runtimeDisplayLabel(runtime)
    : t(($) => $.mirror.title);
  const stateCopy = mirrorStateCopy(t, state);
  const showRetry = state === "transport-failed" ||
    state === "permission-denied" ||
    state === "capture-unavailable";
  const showNatWarning = mirror.turnConfigured === false &&
    (state === "negotiating" || state === "streaming" || state === "transport-failed");
  const isPinned = pinsQuery.data?.some(
    (pin) => pin.item_type === "runtime_mirror" && pin.item_id === runtimeId,
  ) === true;
  const pinActionPending = createPin.isPending || deletePin.isPending;
  const pinActionDisabled = !runtime || !canReadRuntime || pinActionPending;

  const handlePinChange = () => {
    if (isPinned) {
      deletePin.mutate({ itemType: "runtime_mirror", itemId: runtimeId });
      return;
    }
    createPin.mutate({ item_type: "runtime_mirror", item_id: runtimeId });
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <BreadcrumbHeader
        segments={[{ href: paths.runtimes(), label: t(($) => $.page.title) }]}
        leaf={<span className="truncate text-foreground">{runtimeName}</span>}
        actions={
          <>
            <span className="inline-flex items-center gap-1.5 text-caption text-muted-foreground">
              <Wifi aria-hidden="true" className="h-3.5 w-3.5" />
              {t(($) => $.mirror.connection_label)}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground aria-expanded:text-foreground"
              disabled={pinActionDisabled}
              aria-label={isPinned ? t(($) => $.mirror.unpin_tooltip) : t(($) => $.mirror.pin_tooltip)}
              title={isPinned ? t(($) => $.mirror.unpin_tooltip) : t(($) => $.mirror.pin_tooltip)}
              onClick={handlePinChange}
            >
              {isPinned ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />}
            </Button>
          </>
        }
      />

      <main className="min-h-0 flex-1 overflow-y-auto">
        <div className={cn(PAGE_RAIL, PAGE_GUTTER, "py-6")}>
          <div className="mx-auto w-full max-w-5xl space-y-5">
            <div>
              <p className="text-caption uppercase tracking-wider text-muted-foreground">
                {t(($) => $.mirror.eyebrow)}
              </p>
              <h1 className="mt-1 text-title-sm font-semibold">{runtimeName}</h1>
              <p className="mt-1 max-w-2xl text-body text-muted-foreground">
                {t(($) => $.mirror.description)}
              </p>
            </div>

            <section
              aria-label={t(($) => $.mirror.frame_label)}
              className="overflow-hidden rounded-lg border bg-card"
            >
              <div className="aspect-video w-full bg-muted/30">
                {state === "streaming" && mirror.imageUrl ? (
                  <img
                    src={mirror.imageUrl}
                    alt={t(($) => $.mirror.frame_alt, { runtime: runtimeName })}
                    className="h-full w-full object-contain"
                  />
                ) : (
                  <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
                    <Monitor aria-hidden="true" className="h-8 w-8 text-muted-foreground" />
                    <p className="text-body font-medium">{stateCopy.title}</p>
                    <p
                      className="max-w-md text-caption text-muted-foreground"
                      role={isTerminalMirrorState(state) ? "alert" : "status"}
                      aria-live="polite"
                    >
                      {stateCopy.description}
                    </p>
                    {showRetry && (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => setRetryNonce((value) => value + 1)}
                      >
                        <RefreshCw aria-hidden="true" className="h-3.5 w-3.5" />
                        {t(($) => $.mirror.try_again)}
                      </Button>
                    )}
                  </div>
                )}
              </div>
              <div
                className={cn(
                  "flex items-start gap-2 border-t px-4 py-3 text-caption",
                  (state === "transport-failed" || state === "permission-denied") &&
                    "text-destructive",
                  (state === "offline" || state === "old-daemon" || state === "linux-unsupported") &&
                    "text-warning",
                  state !== "transport-failed" &&
                    state !== "permission-denied" &&
                    state !== "offline" &&
                    state !== "old-daemon" &&
                    state !== "linux-unsupported" &&
                    "text-muted-foreground",
                )}
                role={isTerminalMirrorState(state) ? "alert" : "status"}
                aria-live="polite"
              >
                {isTerminalMirrorState(state) ? (
                  <WifiOff aria-hidden="true" className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                ) : (
                  <Wifi aria-hidden="true" className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                )}
                <span>{stateCopy.status}</span>
              </div>
            </section>

            {showNatWarning && (
              <p className="text-caption text-warning" role="status">
                {t(($) => $.mirror.nat_warning)}
              </p>
            )}

            <div className="flex flex-wrap items-center gap-3 text-caption text-muted-foreground">
              <AppLink
                href={paths.runtimeDetail(runtimeId)}
                className="rounded-sm underline-offset-4 hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {t(($) => $.mirror.back_to_runtime)}
              </AppLink>
              <span aria-hidden="true">·</span>
              <span className="font-mono">{runtimeId}</span>
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}
