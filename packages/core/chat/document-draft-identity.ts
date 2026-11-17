import type { DocumentDraft, DocumentRef } from "./doc-mode-store";
import { documentDraftKey, legacyDocumentDraftKey } from "./document-identity";

// The accumulator belongs to the caller's migration, never the live store.
export function adoptDocumentDraftIdentity(doc: DocumentRef, drafts: Record<string, DocumentDraft>): DocumentRef {
  if (doc.kind !== "workspace_file" || !doc.fileId) return doc;
  const previous = legacyDocumentDraftKey(doc);
  const canonical = documentDraftKey(doc);
  const draft = drafts[previous];
  if (!draft) return doc;
  if (drafts[canonical]) {
    // Keep independently edited legacy tabs accessible until their drafts are
    // resolved. Adopting an identity must never choose one draft over another.
    const { fileId: _fileId, ...legacy } = doc;
    return legacy;
  }
  drafts[canonical] = draft;
  delete drafts[previous];
  return doc;
}
