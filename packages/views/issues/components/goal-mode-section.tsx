"use client";

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Target } from "lucide-react";
import { api, ApiError } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import type { Issue } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../../i18n";

interface GoalModeSectionProps {
  issue: Issue;
  wsId: string;
}

export function GoalModeSection({ issue, wsId }: GoalModeSectionProps) {
  const { t } = useT("issues");
  const userId = useAuthStore((state) => state.user?.id);
  const queryClient = useQueryClient();
  const [objective, setObjective] = useState("");
  const goalQuery = useQuery({
    queryKey: ["issues", wsId, "goal", issue.id],
    queryFn: () => api.getIssueGoal(issue.id),
    retry: false,
  });
  const goal = goalQuery.data;
  const goalMissing = goalQuery.error instanceof ApiError && goalQuery.error.status === 404;
  const isMemberOwner = issue.assignee_type === "member" && issue.assignee_id === userId;

  useEffect(() => {
    if (goal) setObjective(goal.objective);
  }, [goal]);

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ["issues", wsId, "goal", issue.id] });
  };
  const saveGoal = useMutation({
    mutationFn: () => api.upsertIssueGoal(issue.id, { objective: objective.trim() }),
    onSuccess: refresh,
  });
  const completeGoal = useMutation({
    mutationFn: () => api.completeIssueGoal(issue.id),
    onSuccess: refresh,
  });
  const disableGoal = useMutation({
    mutationFn: () => api.deleteIssueGoal(issue.id),
    onSuccess: refresh,
  });

  if (goalQuery.isLoading) return null;
  if (!goal && !goalMissing) return null;

  if (!goal) {
    return (
      <div className="space-y-2 rounded-md border border-dashed p-3">
        <div className="flex items-center gap-2 text-caption font-medium">
          <Target className="size-3.5 text-muted-foreground" />
          {t(($) => $.goal_mode.title)}
        </div>
        <Input
          value={objective}
          onChange={(event) => setObjective(event.target.value)}
          placeholder={t(($) => $.goal_mode.placeholder)}
          aria-label={t(($) => $.goal_mode.objective_aria)}
        />
        <Button
          size="sm"
          variant="outline"
          disabled={!objective.trim() || saveGoal.isPending}
          onClick={() => saveGoal.mutate()}
        >
          {t(($) => $.goal_mode.enable)}
        </Button>
      </div>
    );
  }

  const completed = goal.completed_at !== null;
  return (
    <div className="space-y-2 rounded-md border p-3">
      <div className="flex items-center gap-2 text-caption font-medium">
        <Target className="size-3.5 text-primary" />
        {t(($) => $.goal_mode.title)}
      </div>
      <p className="text-body whitespace-pre-wrap">{goal.objective}</p>
      {completed ? (
        <div className="flex items-center gap-1.5 text-caption text-success">
          <CheckCircle2 className="size-3.5" />
          {t(($) => $.goal_mode.completed)}
        </div>
      ) : (
        <p className="text-caption text-muted-foreground">{t(($) => $.goal_mode.review_gate)}</p>
      )}
      <div className="flex flex-wrap gap-2">
        {isMemberOwner && !completed && (
          <Button size="sm" onClick={() => completeGoal.mutate()} disabled={completeGoal.isPending}>
            {t(($) => $.goal_mode.complete)}
          </Button>
        )}
        {!completed && (
          <Button size="sm" variant="ghost" onClick={() => disableGoal.mutate()} disabled={disableGoal.isPending}>
            {t(($) => $.goal_mode.disable)}
          </Button>
        )}
      </div>
    </div>
  );
}
