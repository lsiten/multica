"use client";

import { useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import { Button } from "@multica/ui/components/ui/button";
import { localReviewInventoryPage } from "../../platform/local-review";
import { LocalReviewDialog } from "./local-review-dialog";
import { useT } from "../../i18n";

export function LocalWorktreeReviews({ workspaceId, agentId }: { workspaceId: string; agentId?: string }) {
  return <LocalWorktreeReviewList workspaceId={workspaceId} agentId={agentId} />;
}

function LocalWorktreeReviewList({ workspaceId, agentId }: { workspaceId: string; agentId?: string }) {
  const { t } = useT("issues");
  const userId = useAuthStore((state) => state.user?.id);
  const inventory = useInfiniteQuery({
    queryKey: ["local-review-worktrees", userId, workspaceId, agentId],
    queryFn: ({ pageParam }) => localReviewInventoryPage(pageParam, agentId),
    initialPageParam: 0,
    getNextPageParam: (last, pages) => last.length === 100 ? pages.length * 100 : undefined,
    enabled: !!userId, retry: false, refetchInterval: 30000,
  });
  const [request, setRequest] = useState<LocalReviewRequest | null>(null);
  const rows = inventory.data?.pages.flat().filter((row) => row.workspaceId === workspaceId && (!agentId || row.agentId === agentId)) ?? [];
  return <section className="space-y-2 rounded-lg border p-3">
    <h3 className="text-body font-medium">{t(($) => $.local_review.worktrees)}</h3>
    {inventory.error && <p role="alert">{inventory.error.message}</p>}
    {inventory.isPending && <p role="status">{t(($) => $.local_review.loading)}</p>}
    {!inventory.isPending && !rows.length && <p className="text-caption text-muted-foreground">{t(($) => $.local_review.no_worktrees)}</p>}
    {rows.flatMap((row) => row.repositories.map((path) => <div key={row.taskId + path} className="flex items-center gap-2">
      <span className="min-w-0 flex-1 break-all text-caption">{row.taskName} · {row.runtimeId.slice(0, 8)}<br />{path}</span>
      <Button variant="outline" size="sm" disabled={!row.taskId} onClick={() => setRequest({ task_id: row.taskId, workspace_id: row.workspaceId, runtime_id: row.runtimeId, path, target: "main" })}>{t(($) => $.local_review.open)}</Button>
    </div>))}
    {inventory.hasNextPage && <Button variant="outline" disabled={inventory.isFetchingNextPage} onClick={() => void inventory.fetchNextPage()}>{t(($) => $.local_review.load_more)}</Button>}
    {request && <LocalReviewDialog key={request.path} request={request} onClose={() => setRequest(null)} />}
  </section>;
}
