export interface ProgressScope { type: "issue" | "project"; id: string }
export interface ProgressIssueRef {
  id: string;
  identifier: string;
  title: string;
  status: string;
  status_category: string;
  status_name: string;
  project_id: string | null;
  project_title: string;
}
export interface ProgressRun { id: string; status: string; since: string }
export interface ProgressRequest { id: string; recipient_id: string; needs_me: boolean; expires_at: string }
export interface ProgressEntry {
  issue: ProgressIssueRef;
  priority: string;
  assignee_type: string;
  assignee_id: string | null;
  path: ProgressIssueRef[];
  group: ProgressIssueRef | null;
  reasons: string[];
  needs_me: boolean;
  attention: boolean;
  waiting_children: number;
  direct_blockers: ProgressIssueRef[];
  root_blockers: ProgressIssueRef[];
  blocked_issue_count: number;
  affected_goal_count: number;
  run: ProgressRun | null;
  requests: ProgressRequest[];
  stage: number | null;
  due_date: string | null;
  wait_since: string;
  rank: number;
}
export interface ProgressSummary {
  total: number;
  done: number;
  closed: number;
  open: number;
  open_leaf: number;
  open_parent: number;
  attention: number;
  pending_decisions: number;
}
export interface IssueProgressView {
  workspace_id: string;
  scope: ProgressScope;
  as_of: string;
  version: string;
  summary: ProgressSummary;
  root: ProgressEntry | null;
  items: ProgressEntry[];
  project_requests: ProgressRequest[];
  complete: boolean;
  coverage_reasons: string[];
  next_cursor: string | null;
  has_more: boolean;
  filtered_total: number;
}
export type ProgressFilter = "all" | "blocked" | "review" | "follow_up" | "ready";
export interface ProgressFilters {
  filter?: ProgressFilter;
  assignee_type?: "member" | "agent" | "squad";
  assignee_id?: string;
  mine?: boolean;
  limit?: number;
}
