import { useEffect } from "react";
import { adoptWorkspaceFileDocumentIdentity, updateWorkspaceFileDocumentLocation, type DocumentRef } from "@multica/core/chat";
import type { PagedReviewResponse } from "@multica/core/types/local-review-pages";

export function useWorkspaceFileDocumentIdentity({ workspaceId, document, page, editing }: {
  readonly workspaceId: string;
  readonly document: DocumentRef | null;
  readonly page: PagedReviewResponse | null;
  readonly editing: boolean;
}) {
  useEffect(() => {
    if (!document || !page || !("content" in page) || !("file_id" in page) || !page.file_id) return;
    if (!document.fileId && !editing && page.file_identity_ambiguous !== true)
      adoptWorkspaceFileDocumentIdentity(workspaceId, document, { fileId: page.file_id, directoryId: page.directory_id });
    if (document.fileId && page.directory_revision !== undefined)
      updateWorkspaceFileDocumentLocation(document, { fileId: page.file_id, directoryId: page.directory_id, directoryRevision: page.directory_revision, relativePath: page.path });
  }, [document, editing, page, workspaceId]);
}
