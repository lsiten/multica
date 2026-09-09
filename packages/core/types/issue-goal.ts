/** A task objective protected by the goal-mode review gate. */
export interface IssueGoal {
  objective: string;
  completed_at: string | null;
  completed_by_type: "member" | "agent" | null;
  completed_by_id: string | null;
}

export interface UpsertIssueGoalRequest {
  objective: string;
}
