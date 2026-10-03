import { worktreeArchiveRequestSchema, type WorktreeArchiveResult } from "@multica/core/types/worktree-archives";

export async function requestWorktreeArchive(input: unknown, send: (request: unknown) => Promise<WorktreeArchiveResult[]>): Promise<WorktreeArchiveResult[]> {
  const request = worktreeArchiveRequestSchema.parse(input);
  return send({
    action: request.action, workspace_id: request.workspaceId,
    ...(request.action === "archive" ? { operation_id: request.operationId, selections: request.selections.map(({ environmentId, revision }) => ({ environment_id: environmentId, revision })) } : {}),
    ...(request.action === "restore" ? { archive_id: request.archiveId } : {}),
  });
}
