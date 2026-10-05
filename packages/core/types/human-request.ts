export interface HumanRequestChoice {
  id: string;
  label: string;
  recommended?: boolean;
}

export interface HumanRequestPayload {
  key: string;
  kind: "confirmation" | "choice" | "input" | "manual" | "unknown";
  title: string;
  steps: string[];
  action_label: string;
  next: string;
  impact?: string;
  choices: HumanRequestChoice[];
  input_label?: string;
  verification?: string;
  details?: string;
}

export interface HumanRequestAnswer {
  revision: number;
  decision: "approve" | "reject" | "choice" | "input" | "completed";
  answer?: string;
}

export interface HumanRequest {
  id: string;
  workspace_id: string;
  source_task_id: string;
  agent_id: string;
  recipient_id: string;
  issue_id: string | null;
  chat_session_id: string | null;
  project_id: string | null;
  revision: number;
  status: "pending" | "answered" | "declined" | "cancelled" | "expired" | "unknown";
  payload: HumanRequestPayload;
  response: HumanRequestAnswer | null;
  response_task_id: string | null;
  can_respond: boolean;
  expires_at: string;
}
