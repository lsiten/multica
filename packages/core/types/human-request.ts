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
  response_mode?: "card_only" | "chat_or_card";
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

export interface HumanTextReplyResult {
  request: HumanRequest;
  reply: { channel: string; text: string; reply_id?: string; created_at?: string } | null;
  task_id: string | null;
}

export interface HumanResponseReceipt { request_id: string; revision: number; label: string }
