import type { DocumentRef } from "./doc-mode-store";

export function documentCommentScopeKey(workspaceId: string, resource: DocumentRef, parentId?: string): string {
  switch (resource.kind) {
    case "issue":
    case "attachment":
      return JSON.stringify([workspaceId, resource.kind, resource.id, parentId ?? null]);
    case "workspace_file":
      return JSON.stringify([workspaceId, resource.kind, resource.fileId ?? null, resource.fileId ? null : resource.taskId ?? null, resource.fileId ? null : resource.projectResourceId ?? null, resource.fileId ? null : resource.workDir ?? null, resource.fileId ? null : resource.relativePath ?? null, parentId ?? null]);
  }
}
