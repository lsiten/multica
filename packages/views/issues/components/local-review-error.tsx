import { CircleAlert } from "lucide-react";
import { useT } from "../../i18n";

function reviewErrorDetail(error: Error): string {
  return error.message.replace(/^Error invoking remote method ['"]daemon:read-local-review(?:-branches|-page)?['"]:\s*(?:Error:\s*)?/, "");
}

export function reviewErrorKind(error: Error) {
  const detail = reviewErrorDetail(error);
  if (detail === "target must be an existing local branch") return "target";
  if (detail.startsWith("review output exceeds 8 MiB")) return "size";
  if (detail.startsWith("directory has no matching runtime task binding")) return "binding";
  if (detail === "local_review_upgrade_required") return "upgrade";
  if (detail === "local_review_paging_upgrade_required") return "paging";
  if (error.name === "TimeoutError" || detail === "API error: 504") return "timeout";
  return "unknown";
}

export function LocalReviewError({ error, id }: { error: Error; id: string }) {
  const { t } = useT("issues");
  const messages = {
    target: t(($) => $.local_review.error_target),
    size: t(($) => $.local_review.error_size),
    binding: t(($) => $.local_review.error_binding),
    upgrade: t(($) => $.local_review.upgrade_required),
    paging: t(($) => $.local_review.paging_upgrade),
    timeout: t(($) => $.local_review.request_timeout),
    unknown: t(($) => $.local_review.error_generic),
  };
  return <div role="alert" id={id} className="flex gap-2 rounded-lg border border-destructive/20 bg-destructive/5 p-3 text-caption">
    <CircleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-destructive" />
    <div className="min-w-0 space-y-2">
      <p className="font-medium text-destructive">{messages[reviewErrorKind(error)]}</p>
      <details className="text-muted-foreground">
        <summary className="cursor-pointer focus-visible:outline focus-visible:outline-ring">{t(($) => $.local_review.error_details)}</summary>
        <pre className="mt-2 max-h-32 overflow-auto whitespace-pre-wrap break-words font-mono">{reviewErrorDetail(error)}</pre>
      </details>
    </div>
  </div>;
}
