"use client";

import { useState } from "react";
import { Globe } from "lucide-react";
import type { RuntimeDevice } from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";
import {
  runtimeMirrorAvailability,
  RUNTIME_MIRROR_AVAILABILITY,
  isRuntimeUsableForUser,
  runtimeDisplayLabel,
  VSCREEN_CAPABILITIES,
} from "@multica/core/runtimes";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";

export interface RuntimeMirrorActionProps {
  readonly runtime: RuntimeDevice;
  readonly compact?: boolean;
}

export function RuntimeMirrorAction({
  runtime,
  compact = false,
}: RuntimeMirrorActionProps) {
  const { t } = useT("runtimes");
  const paths = useWorkspacePaths();
  const currentUserId = useAuthStore((state) => state.user?.id ?? null);
  const readable =
    !!currentUserId && isRuntimeUsableForUser(runtime, currentUserId);
  const capabilities = runtime.metadata.capabilities;
  const video =
    Array.isArray(capabilities) &&
    capabilities.includes(VSCREEN_CAPABILITIES.video) &&
    capabilities.includes(VSCREEN_CAPABILITIES.viewerGrant);
  const legacy = runtimeMirrorAvailability(runtime);
  const availability = {
    ...legacy,
    enabled:
      readable &&
      runtime.status === "online" &&
      !!runtime.daemon_id &&
      (video || legacy.enabled),
  };

  const reason = availability.enabled
    ? null
    : availability.kind === RUNTIME_MIRROR_AVAILABILITY.offline
      ? t(($) => $.detail.mirror_reason.offline)
      : availability.kind === RUNTIME_MIRROR_AVAILABILITY.linuxUnsupported
        ? t(($) => $.detail.mirror_reason.linux)
        : availability.kind === RUNTIME_MIRROR_AVAILABILITY.unsupportedPlatform
          ? t(($) => $.detail.mirror_reason.platform)
          : t(($) => $.detail.mirror_reason.old_daemon);

  if (!readable) return null;

  return (
    <span className="inline-flex items-center gap-2">
      {availability.enabled ? (
        <Button
          variant="outline"
          size={compact ? "icon-sm" : "sm"}
          render={<AppLink href={paths.runtimeMirror(runtime.id)} />}
          nativeButton={false}
          aria-label={t(($) => $.detail.open_mirror)}
        >
          <Globe aria-hidden="true" className="h-3.5 w-3.5" />
          {!compact && t(($) => $.detail.open_mirror)}
        </Button>
      ) : (
        <Button
          variant="outline"
          size={compact ? "icon-sm" : "sm"}
          disabled
          aria-label={t(($) => $.detail.open_mirror)}
          type="button"
          title={reason ?? undefined}
        >
          <Globe aria-hidden="true" className="h-3.5 w-3.5" />
          {!compact && t(($) => $.detail.open_mirror)}
        </Button>
      )}
      {reason && !compact && (
        <span className="hidden text-caption text-muted-foreground sm:inline">
          {reason}
        </span>
      )}
    </span>
  );
}

export function MachineMirrorAction({
  runtimes,
}: {
  readonly runtimes: readonly RuntimeDevice[];
}) {
  const currentUserId = useAuthStore((state) => state.user?.id ?? null);
  const { t } = useT("runtimes");
  const [selectedId, setSelectedId] = useState("");
  const readable = runtimes.filter(
    (entry) =>
      entry.runtime_mode === "local" &&
      !!currentUserId &&
      isRuntimeUsableForUser(entry, currentUserId),
  );
  const runtime =
    readable.length === 1
      ? readable[0]
      : readable.find((entry) => entry.id === selectedId);
  if (!readable.length) return null;
  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      {readable.length > 1 && (
        <select
          aria-label={t(($) => $.vscreen.select_runtime)}
          value={selectedId}
          onChange={(event) => setSelectedId(event.target.value)}
          className="h-8 max-w-56 rounded-md border bg-background px-2 text-caption focus-visible:ring-2 focus-visible:ring-ring"
        >
          <option value="">{t(($) => $.vscreen.select_runtime)}</option>
          {readable.map((entry) => (
            <option key={entry.id} value={entry.id}>
              {runtimeDisplayLabel(entry)}
            </option>
          ))}
        </select>
      )}
      {runtime && <RuntimeMirrorAction runtime={runtime} />}
    </span>
  );
}
