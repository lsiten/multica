"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Code2, FolderGit2, GitBranch, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { issueKeys } from "@multica/core/issues/queries";
import { resolveWorkdirCopyTarget } from "@multica/core/issues";
import type { AgentTask } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

function latestReviewableTask(tasks: readonly AgentTask[]): AgentTask | undefined {
  return tasks
    .filter((task) => task.agent_id && (task.branch_name?.trim() || task.work_dir?.trim()))
    .toSorted((a, b) => b.created_at.localeCompare(a.created_at))[0];
}

/** Shows the task's latest durable code location and starts a review run there. */
export function CodeReviewContextSection({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const [reviewing, setReviewing] = useState(false);
  const { data: tasks = [] } = useQuery({
    queryKey: issueKeys.tasks(issueId),
    queryFn: () => api.listTasksByIssue(issueId),
    staleTime: 30_000,
  });
  const task = useMemo(() => latestReviewableTask(tasks), [tasks]);
  const worktree = useMemo(() => resolveWorkdirCopyTarget(tasks), [tasks]);
  if (!task || !worktree) return null;

  const branch = task.branch_name?.trim() || worktree.branchName;
  const path = worktree.relativePath || worktree.path;
  const requestReview = async () => {
    if (reviewing) return;
    setReviewing(true);
    try {
      await api.createComment(
        issueId,
        `[@Review](mention://agent/${task.agent_id}) 请 Review 此任务关联 worktree 的代码改动。\n${branch ? `branch: ${branch}\n` : ""}worktree: ${worktree.path}\n对比任务开始前的基线，检查功能正确性、权限、错误处理和测试；将问题按严重级别回复到本任务，不要直接修改代码。`,
      );
      toast.success(t(($) => $.execution_log.review_requested));
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t(($) => $.execution_log.review_request_failed),
      );
    } finally {
      setReviewing(false);
    }
  };

  return (
    <section className="mb-4 rounded-lg border bg-card p-3">
      <div className="flex items-center gap-1.5 text-caption font-medium">
        <Code2 className="size-3.5 text-muted-foreground" aria-hidden="true" />
        {t(($) => $.execution_log.code_context)}
      </div>
      {branch && (
        <div className="mt-2 flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground">
          <GitBranch className="size-3.5 shrink-0" aria-hidden="true" />
          <span className="truncate" title={branch}>{branch}</span>
        </div>
      )}
      <div className="mt-1 flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground">
        <FolderGit2 className="size-3.5 shrink-0" aria-hidden="true" />
        <span className="truncate" title={worktree.path}>{path}</span>
      </div>
      <Button
        className="mt-3 w-full"
        size="sm"
        variant="outline"
        onClick={() => void requestReview()}
        disabled={reviewing}
      >
        {reviewing ? <Loader2 className="size-3.5 animate-spin" /> : <Code2 className="size-3.5" />}
        {t(($) => $.execution_log.review_changes_tooltip)}
      </Button>
    </section>
  );
}
