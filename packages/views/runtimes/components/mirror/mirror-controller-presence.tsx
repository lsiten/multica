"use client";

import type {
  VscreenScope,
  VscreenSourceDescriptor,
} from "@multica/core/types";
import { useT } from "../../../i18n";
import { useMirrorControllers } from "./use-mirror-controllers";

export function MirrorControllerPresence({
  scope,
  enabled,
  catalog,
}: {
  readonly scope: VscreenScope;
  readonly enabled: boolean;
  readonly catalog: readonly VscreenSourceDescriptor[];
}) {
  const { t } = useT("runtimes");
  const { controllers } = useMirrorControllers({ scope, enabled, catalog });
  if (controllers.length === 0) return null;

  return (
    <div
      className="flex flex-wrap gap-x-3 gap-y-1 border-b bg-muted/30 px-3 py-1.5 text-caption text-muted-foreground"
      role="status"
      aria-live="polite"
    >
      {controllers.map((controller) => (
        <span key={controller.viewerId}>
          {controller.self
            ? t(($) => $.vscreen.controller_self, {
                source: controller.sourceLabel,
              })
            : t(($) => $.vscreen.controller_active, {
                name: controller.name,
                source: controller.sourceLabel,
              })}
        </span>
      ))}
    </div>
  );
}
