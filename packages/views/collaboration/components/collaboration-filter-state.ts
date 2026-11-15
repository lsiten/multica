export const COLLABORATION_ACTIVITIES = ["active", "all", "ended"] as const;
export type CollaborationActivity = (typeof COLLABORATION_ACTIVITIES)[number];

export const COLLABORATION_VIEWS = ["graph", "list"] as const;
export type CollaborationView = (typeof COLLABORATION_VIEWS)[number];

export const COLLABORATION_RUN_STATUSES = [
  "queued",
  "dispatched",
  "running",
  "waiting_local_directory",
  "completed",
  "failed",
  "cancelled",
] as const;

export function collaborationActivityFromParam(
  value: string,
): CollaborationActivity {
  return COLLABORATION_ACTIVITIES.find((activity) => activity === value)
    ?? "active";
}

export function collaborationViewFromParam(value: string): CollaborationView {
  return COLLABORATION_VIEWS.find((view) => view === value) ?? "graph";
}

export function collaborationStatusFromParam(value: string): string {
  return COLLABORATION_RUN_STATUSES.find((status) => status === value) ?? "";
}

export function collaborationDateBoundary(
  value: string,
  endOfDay: boolean,
): string | undefined {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return undefined;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const date = new Date(
    year,
    month - 1,
    day,
    endOfDay ? 23 : 0,
    endOfDay ? 59 : 0,
    endOfDay ? 59 : 0,
    endOfDay ? 999 : 0,
  );
  if (
    date.getFullYear() !== year ||
    date.getMonth() !== month - 1 ||
    date.getDate() !== day
  ) {
    return undefined;
  }
  return date.toISOString();
}
