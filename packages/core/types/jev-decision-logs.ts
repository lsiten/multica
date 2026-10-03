export type JevDecisionResult = "running" | "success" | "error" | "rejected" | "unknown";
export type JevDecisionSource = "agent_context" | "local" | "remote" | "system_one" | "unknown";

export interface JevDecisionLogSummary {
  id: string;
  workspace_id: string;
  task_id: string;
  agent_id: string;
  agent_name: string;
  issue_identifier: string;
  tool: string;
  source: JevDecisionSource;
  model: string;
  result_class: JevDecisionResult;
  error_code: string;
  started_at: string;
  duration_ms: number;
}

export interface JevDecisionRequestLog {
  attempt: number;
  variant: string;
  http_status: number;
  result_class: string;
  duration_ms: number;
  input: string;
  output: string;
  response_incomplete: boolean;
}

export interface JevDecisionLogDetail extends JevDecisionLogSummary {
  model_revision: string;
  config_revision: number;
  device: string;
  completed_at: string | null;
  input: string;
  output: string;
  requests: JevDecisionRequestLog[];
}

export interface JevDecisionLogFilters {
  q?: string;
  source?: string;
  result_class?: string;
  agent?: string;
  from?: string;
  to?: string;
  as_of?: string;
  limit?: number;
  offset?: number;
}

export interface JevDecisionLogListResponse {
  items: JevDecisionLogSummary[];
  total: number;
  limit: number;
  offset: number;
  as_of: string;
}
