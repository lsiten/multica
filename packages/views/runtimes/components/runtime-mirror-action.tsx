"use client";

import { Globe } from "lucide-react";
import type { RuntimeDevice } from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";
import {
  runtimeMirrorAvailability,
  RUNTIME_MIRROR_AVAILABILITY,
  selectRuntimeForMirror,
} from "@multica/core/runtimes";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";

export interface RuntimeMirrorActionProps {
  readonly runtime: RuntimeDevice;
}

export function RuntimeMirrorAction({
  runtime,
}: RuntimeMirrorActionProps) {
  const { t } = useT("runtimes");
  const paths = useWorkspacePaths();
  const availability = runtimeMirrorAvailability(runtime);

  const reason = availability.enabled
    ? null
    : availability.kind === RUNTIME_MIRROR_AVAILABILITY.offline
      ? t(($) => $.detail.mirror_reason.offline)
      : availability.kind === RUNTIME_MIRROR_AVAILABILITY.linuxUnsupported
        ? t(($) => $.detail.mirror_reason.linux)
        : availability.kind === RUNTIME_MIRROR_AVAILABILITY.unsupportedPlatform
          ? t(($) => $.detail.mirror_reason.platform)
          : t(($) => $.detail.mirror_reason.old_daemon);

  return (
    <span className="inline-flex items-center gap-2">
      {availability.enabled ? (
        <Button
          variant="outline"
          size="sm"
          render={<AppLink href={paths.runtimeMirror(runtime.id)} />}
          nativeButton={false}
        >
          <Globe aria-hidden="true" className="h-3.5 w-3.5" />
          {t(($) => $.detail.open_mirror)}
        </Button>
      ) : (
        <Button
          variant="outline"
          size="sm"
          disabled
          type="button"
          title={reason ?? undefined}
        >
          <Globe aria-hidden="true" className="h-3.5 w-3.5" />
          {t(($) => $.detail.open_mirror)}
        </Button>
      )}
      {reason && (
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
  const runtime = selectRuntimeForMirror(runtimes, currentUserId);

  if (!runtime || runtime.runtime_mode !== "local") return null;
  return <RuntimeMirrorAction runtime={runtime} />;
}
