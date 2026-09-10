"use client";

import { useId } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import { Button } from "@multica/ui/components/ui/button";
import { probeReviewRepositories } from "../../platform/local-review-probe";
import { useT } from "../../i18n";

export function LocalReviewEntry({ request, onOpen, className, labelSuffix }: {
  readonly request: LocalReviewRequest;
  readonly onOpen: (request: LocalReviewRequest) => void;
  readonly className?: string;
  readonly labelSuffix?: string;
}) {
  const { t } = useT("issues");
  const userId = useAuthStore((state) => state.user?.id);
  const statusId = useId();
  const probe = useQuery({
    queryKey: ["review-entry-repositories", userId, request.workspace_id, request.runtime_id, request.task_id, request.path],
    queryFn: ({ signal }) => probeReviewRepositories(request, signal),
    enabled: !!userId && !!request.task_id && !!request.path,
    retry: false, networkMode: "always", staleTime: 30000,
  });
  const ready = probe.isSuccess && !probe.isFetching && probe.data.repositories.length > 0;
  const reason = probe.isFetching || probe.isPending ? t(($) => $.local_review.entry_checking) : probe.isError ? t(($) => $.local_review.entry_failed) : t(($) => $.local_review.entry_empty);
  return <div className={className}>
    <Button className="w-full" variant="outline" size="sm" disabled={!ready} aria-describedby={!ready ? statusId : undefined} onClick={() => { if (ready) onOpen(request); }}>{t(($) => $.local_review.open)}{labelSuffix ? ` · ${labelSuffix}` : ""}</Button>
    {!ready && <p id={statusId} role="status" className="mt-1 text-caption text-muted-foreground">{reason}</p>}
    {!probe.isPending && !probe.isFetching && !ready && <Button variant="ghost" size="sm" onClick={() => { void probe.refetch(); }}>{t(($) => $.local_review.entry_recheck)}</Button>}
  </div>;
}
