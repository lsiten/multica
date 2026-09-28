"use client";

import { useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import { Button } from "@multica/ui/components/ui/button";
import { localReviewInventoryPage } from "../../platform/local-review";
import { LocalReviewDialog } from "./local-review-dialog";
import { LocalReviewEntry } from "./local-review-entry";
import { useT } from "../../i18n";
import { compareWorktreeLifecycle, worktreeActions } from "@multica/core/types/worktree-lifecycle";
import { WorktreeActionLabel, WorktreeLifecycle, WorktreeRepositoryStatus } from "./worktree-lifecycle";

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
  const rows = inventory.data?.pages.flat().filter((row) => row.repositories.length > 0 && row.workspaceId === workspaceId && (!agentId || row.agentId === agentId)).toSorted(compareWorktreeLifecycle) ?? [];
  return <section className="space-y-2 rounded-lg border p-3">
    <h3 className="text-body font-medium">{t(($) => $.local_review.worktrees)}</h3>
    {inventory.error && <p role="alert">{inventory.error.message}</p>}
    {inventory.isPending && <p role="status">{t(($) => $.local_review.loading)}</p>}
    {!inventory.isPending && !rows.length && <p className="text-caption text-muted-foreground">{t(($) => $.local_review.no_worktrees)}</p>}
    {worktreeActions.map((action) => {
      const group = rows.filter((row) => (row.nextAction ?? "unknown") === action);
      return group.length > 0 && <div key={action} className="space-y-3 border-t pt-2"><h4 className="text-caption font-medium"><WorktreeActionLabel action={action} /> · {group.length}</h4>{group.map((row) => <div key={row.taskId} className="space-y-2">
        <p className="break-all text-caption">{row.taskName} · {row.runtimeId.slice(0, 8)}</p>
        <WorktreeLifecycle row={row} />
        {row.repositories.map((path) => {
          const repository = row.repositoryDetails?.find((entry) => entry.path === path);
          return <div key={path} className="flex flex-wrap items-start gap-2"><div className="min-w-0 flex-1"><p className="break-all text-caption">{path}</p><WorktreeRepositoryStatus repository={repository} /></div><LocalReviewEntry className="max-w-64" request={{ task_id: row.taskId, workspace_id: row.workspaceId, runtime_id: row.runtimeId, path, target: repository?.target ?? "" }} onOpen={setRequest} /></div>;
        })}
      </div>)}</div>;
    })}
    {inventory.hasNextPage && <Button variant="outline" disabled={inventory.isFetchingNextPage} onClick={() => void inventory.fetchNextPage()}>{t(($) => $.local_review.load_more)}</Button>}
    {request && rows.some((row) => row.taskId === request.task_id && row.repositories.includes(request.path)) && <LocalReviewDialog key={request.path} request={request} onClose={() => setRequest(null)} />}
  </section>;
}
