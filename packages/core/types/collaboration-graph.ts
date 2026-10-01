export type CollaborationGraphNodeType = "project" | "task" | "agent";

export type CollaborationGraphEdgeType =
  | "delegated"
  | "retry"
  | "rerun"
  | "assigned"
  | "produced"
  | "derived_from"
  | "coordinate"
  | "handoff"
  | "review"
  | "accept";

export interface CollaborationGraphNode {
  id: string;
  type: CollaborationGraphNodeType;
  label: string;
  status?: string | null;
  data?: Record<string, unknown>;
}

export interface CollaborationGraphEdge {
  id: string;
  from: string;
  to: string;
  type: CollaborationGraphEdgeType;
  count: number;
  active_count: number;
  evidence_count: number;
  last_event_at: string | null;
}

export type CollaborationGraphCoverage = "complete" | "partial" | "empty";

export interface ProjectCollaborationGraphSummary {
  task_count: number;
  agent_count: number;
  active_count: number;
  coverage: CollaborationGraphCoverage;
}

export interface ProjectCollaborationGraphResponse {
  project_id: string;
  nodes: CollaborationGraphNode[];
  edges: CollaborationGraphEdge[];
  summary: ProjectCollaborationGraphSummary;
  as_of: string;
  limit: number;
  offset: number;
  has_more: boolean;
  truncated?: boolean;
  coverage_reasons?: string[];
}

export interface ProjectCollaborationEvidence {
  task_id: string;
  source_task_id: string | null;
  agent_id: string;
  source_agent_id: string | null;
  relation_type: string;
  status: string;
  issue_id: string | null;
  squad_id: string | null;
  trigger_comment_id: string | null;
  created_at: string;
  started_at: string | null;
  completed_at: string | null;
  event_count: number;
  retry_of_task_id?: string | null;
  rerun_of_task_id?: string | null;
}

export interface ProjectCollaborationEvidenceResponse {
  project_id: string;
  evidence: ProjectCollaborationEvidence[];
  total: number;
  limit: number;
  offset: number;
  has_more: boolean;
  as_of: string;
  truncated: boolean;
}

export type SquadCollaborationRelationType =
  | "coordinate"
  | "handoff"
  | "review"
  | "accept";

export interface SquadCollaborationMember {
  member_id: string;
  member_type: "agent" | "member";
  label: string;
  role: string;
}

export interface SquadCollaborationRelation {
  id: string;
  from_member_id: string;
  to_member_id: string;
  from_member_type: "agent" | "member";
  to_member_type: "agent" | "member";
  type: SquadCollaborationRelationType;
  label: string;
  trigger: string;
  deliverables: string[];
  acceptance: string;
}

export interface SquadCollaborationGraphResponse {
  squad_id: string;
  revision: number;
  updated_at: string | null;
  members: SquadCollaborationMember[];
  relations: SquadCollaborationRelation[];
  derived_relations?: SquadCollaborationRelation[];
}

export interface UpdateSquadCollaborationGraphRequest {
  expected_revision: number;
  relations: Array<Omit<SquadCollaborationRelation, "id"> & { id?: string }>;
}
