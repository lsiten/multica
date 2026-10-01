export interface ProjectGraphEvent {
  id: string;
  project_id: string;
  task_id: string;
  event_type: string;
  node_id: string;
  data: Record<string, unknown> | null;
  created_at: string;
}

export interface ProjectGraphEventsResponse {
  events: ProjectGraphEvent[];
  limit: number;
  offset: number;
}
