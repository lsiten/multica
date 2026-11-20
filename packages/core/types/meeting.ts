export type MeetingStatus =
  | "recording"
  | "paused"
  | "finalizing"
  | "organizing"
  | "ready"
  | "failed"
  | "cancelled";

export type MeetingSourceType = "workspace" | "chat" | "group";

export type MeetingSegment = {
  readonly id: string;
  readonly sequence: number;
  readonly start_ms: number;
  readonly end_ms: number;
  readonly speaker_label: string;
  readonly text: string;
  readonly is_final: boolean;
  readonly confidence?: number;
};

export type Meeting = {
  readonly id: string;
  readonly workspace_id: string;
  readonly creator_id: string;
  readonly source_type: MeetingSourceType;
  readonly source_chat_session_id?: string;
  readonly source_agent_id?: string;
  readonly runtime_id?: string;
  readonly status: MeetingStatus;
  readonly language: string;
  readonly title: string;
  readonly timezone: string;
  readonly started_at: string;
  readonly ended_at?: string;
  readonly audio_retention: "discard" | "retain";
  readonly project_id?: string;
  readonly topic: string;
  readonly transcript?: string;
  readonly summary?: string;
  readonly decisions: readonly string[];
  readonly action_items: readonly string[];
  readonly classification: Readonly<Record<string, unknown>>;
  readonly failure_reason?: string;
  readonly archived_at?: string;
  readonly created_at: string;
  readonly updated_at: string;
  readonly segments?: readonly MeetingSegment[];
};

export type CreateMeetingRequest = {
  readonly source_type?: MeetingSourceType;
  readonly source_chat_session_id?: string;
  readonly source_agent_id?: string;
  readonly runtime_id?: string;
  readonly language?: string;
  readonly timezone?: string;
};

export type AppendMeetingSegmentRequest = {
  readonly sequence: number;
  readonly start_ms: number;
  readonly end_ms: number;
  readonly speaker_label?: string;
  readonly text: string;
  readonly is_final: boolean;
  readonly confidence?: number;
};

export type UpdateMeetingRequest = {
  readonly title?: string;
  readonly topic?: string;
  readonly project_id?: string | null;
  readonly summary?: string;
  readonly transcript?: string;
  readonly decisions?: readonly string[];
  readonly action_items?: readonly string[];
};
