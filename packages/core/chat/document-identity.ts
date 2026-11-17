import type { DocumentRef } from "./doc-mode-store";

export function documentDraftKey(doc: DocumentRef): string {
  if (doc.kind === "workspace_file" && doc.fileId) return `workspace_file:${doc.fileId}`;
  return legacyDocumentDraftKey(doc);
}

export function legacyDocumentDraftKey(doc: DocumentRef): string {
  return [doc.kind, doc.id, doc.taskId ?? "", doc.projectResourceId ?? "", doc.relativePath ?? ""].join(":");
}
