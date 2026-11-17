"use client";

import { useQuery } from "@tanstack/react-query";
import { issueDetailOptions } from "../issues/queries";
import { documentContentVersionCurrentOptions } from "./document-content-version-queries";
import type { DocumentRef } from "./doc-mode-store";

interface DocumentCurrentContent {
  readonly content: string | null;
  readonly content_url: string | null;
  readonly content_type: string;
  /** Revision used to protect writes, independent of the optional history. */
  readonly revision: number | undefined;
  readonly owner_revision: number | undefined;
}

/** Current issue bodies exist before their first historical checkpoint. */
export function useDocumentCurrentContent(wsId: string, ref: DocumentRef, options?: { loadAttachments?: boolean }) {
  const issue = useQuery({
    ...issueDetailOptions(wsId, ref.id),
    enabled: !!wsId && !!ref.id && ref.kind === "issue",
    staleTime: 30_000,
    select: (owner): DocumentCurrentContent => ({
      content: owner.description ?? "",
      content_url: null,
      content_type: "text/markdown",
      revision: owner.revision,
      owner_revision: owner.revision,
    }),
  });
  const attachment = useQuery({
    ...documentContentVersionCurrentOptions(wsId, ref, options),
    enabled: !!wsId && !!ref.id && ref.kind === "attachment" && options?.loadAttachments !== false,
    select: (version): DocumentCurrentContent => ({
      content: version.content ?? null,
      content_url: version.content_url ?? null,
      content_type: version.content_type,
      revision: version.revision,
      owner_revision: version.owner_revision ?? version.revision,
    }),
  });
  switch (ref.kind) {
    case "issue": return issue;
    case "attachment":
    case "workspace_file": return attachment;
    default: {
      const unhandled: never = ref.kind;
      return unhandled;
    }
  }
}
