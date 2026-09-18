import type { VscreenCommandKind } from "@multica/core/types";
import type { deriveVscreenAccess } from "@multica/core/runtimes";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../../i18n";
export function MirrorCommandControls({
  access,
  state,
  onCommand,
  onRefresh,
}: {
  readonly access: ReturnType<typeof deriveVscreenAccess>;
  readonly state: {
    readonly pending: boolean;
    readonly uncertain: boolean;
    readonly attempted: boolean;
    readonly succeeded: boolean;
  };
  readonly onCommand: (kind: VscreenCommandKind) => void;
  readonly onRefresh: () => void;
}) {
  const { t } = useT("runtimes");
  return (
    <div className="flex flex-wrap items-center gap-2 border-t p-3">
      <Button
        size="sm"
        variant="outline"
        disabled={!access.canEnable || state.pending || state.uncertain}
        onClick={() => onCommand("enable")}
      >
        {t(($) => $.vscreen.enable)}
      </Button>
      <Button
        size="sm"
        variant="outline"
        disabled={!access.canDisable || state.pending || state.uncertain}
        onClick={() => onCommand("disable")}
      >
        {t(($) => $.vscreen.disable)}
      </Button>

      {state.attempted && (
        <span role="status" className="text-caption text-muted-foreground">
          {state.pending
            ? t(($) => $.vscreen.pending)
            : state.succeeded
              ? t(($) => $.vscreen.succeeded)
              : t(($) => $.vscreen.command_failed)}
        </span>
      )}
      {state.uncertain && (
        <Button size="sm" variant="ghost" onClick={onRefresh}>
          {t(($) => $.vscreen.retry)}
        </Button>
      )}
    </div>
  );
}
