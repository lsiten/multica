export type JevSource = "agent_context" | "local" | "remote";
export type JevQuestionType = "choice" | "score" | "noul";

export interface WorkspaceJevConfig {
  source: JevSource;
  model_id?: string;
  model_revision?: string;
  device?: "auto" | "cpu" | "cuda" | "mps";
  endpoint?: string;
  credential_env?: string;
  timeout_seconds: number;
  revision: number;
}

export interface WorkspaceJevConfigResponse {
  workspace_id: string;
  config: WorkspaceJevConfig;
  revision: number;
}

export interface UpdateWorkspaceJevConfigRequest {
  config: WorkspaceJevConfig;
  revision: number;
}
