import { useT } from "@multica/views/i18n";
import type { DaemonStatus } from "../../../shared/daemon-types";

export function JevReportQueue({ status, showHint = false }: {
  readonly status: DaemonStatus;
  readonly showHint?: boolean;
}) {
  const { t } = useT("settings");
  const queue = status.jevDecisionReports;
  if (!queue || (queue.pendingCount === 0 && queue.failedCount === 0)) return null;
  return (
    <div className="min-w-0 text-caption">
      <p className={queue.failedCount > 0 ? "text-destructive" : "text-muted-foreground"}>
        {t(($) => $.desktop.daemon.jev_reports, { pending: queue.pendingCount, failed: queue.failedCount })}
      </p>
      {showHint && (
        <p className="mt-1 text-muted-foreground">
          {t(($) => $.desktop.daemon.jev_reports_hint)}
        </p>
      )}
    </div>
  );
}
