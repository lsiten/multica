export type ProcessCategory =
  | "daemon"
  | "task_worker"
  | "ai"
  | "application"
  | "gateway"
  | "environment"
  | "mirror"
  | "mcp"
  | "jev";

export const PROCESS_CATEGORIES: readonly ProcessCategory[] = [
  "daemon",
  "task_worker",
  "ai",
  "application",
  "gateway",
  "environment",
  "mirror",
  "mcp",
  "jev",
];

export function processCategoryFromParam(
  value: string,
): "all" | ProcessCategory {
  return PROCESS_CATEGORIES.find((category) => category === value) ?? "all";
}
