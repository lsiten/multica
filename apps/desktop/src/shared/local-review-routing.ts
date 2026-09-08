import { localReviewRuntimeHealthSchema, type LocalReviewRequest } from "@multica/core/types/local-review";

export function ownsReviewRuntime(health: unknown, request: LocalReviewRequest, profile: string): boolean {
  const parsed = localReviewRuntimeHealthSchema.safeParse(health);
  return parsed.success && parsed.data.profile === profile && !!request.runtime_id &&
    parsed.data.workspaces.some((workspace) => workspace.id === request.workspace_id && workspace.runtimes.includes(request.runtime_id ?? ""));
}
