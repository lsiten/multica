import { z } from "zod";
import { DocumentResourceSchema } from "./document-reference";
import type { DocumentDraft, DocumentRef } from "./doc-mode-store";
import { documentDraftKey } from "./document-identity";
import { adoptDocumentDraftIdentity } from "./document-draft-identity";

const RefSchema = DocumentResourceSchema.extend({ id: z.string().min(1) });
const DraftSchema = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("version"), content: z.string(), baseContent: z.string(), baseRevision: z.number().int().positive(), contentType: z.string().min(1) }),
  z.object({ kind: z.literal("workspace_file"), content: z.string(), baseContent: z.string() }),
]);
const StateSchema = z.object({
  activeBySession: z.record(z.string(), z.unknown()).optional(),
  openBySession: z.record(z.string(), z.unknown()).optional(),
  draftsByDocument: z.record(z.string(), z.unknown()).optional(),
});

export function restoreDocModeState(value: unknown) {
  const activeBySession: Record<string, DocumentRef | null> = {};
  const openBySession: Record<string, DocumentRef[]> = {};
  const draftsByDocument: Record<string, DocumentDraft> = {};
  const parsed = StateSchema.safeParse(value);
  if (!parsed.success) return { activeBySession, openBySession, draftsByDocument };
  for (const [key, raw] of Object.entries(parsed.data.draftsByDocument ?? {})) {
    const draft = DraftSchema.safeParse(raw);
    if (draft.success) draftsByDocument[key] = draft.data;
  }
  for (const [session, raw] of Object.entries(parsed.data.openBySession ?? {})) {
    if (!Array.isArray(raw)) continue;
    const opened = new Map<string, DocumentRef>();
    for (const entry of raw) {
      const doc = RefSchema.safeParse(entry);
      if (doc.success) {
        const ref = adoptDocumentDraftIdentity(doc.data, draftsByDocument);
        opened.set(documentDraftKey(ref), ref);
      }
    }
    openBySession[session] = [...opened.values()];
  }
  for (const [session, raw] of Object.entries(parsed.data.activeBySession ?? {})) {
    const doc = RefSchema.safeParse(raw);
    const ref = doc.success ? adoptDocumentDraftIdentity(doc.data, draftsByDocument) : null;
    activeBySession[session] = ref;
    if (!ref) continue;
    const opened = openBySession[session] ?? [];
    const key = documentDraftKey(ref);
    openBySession[session] = opened.some((entry) => documentDraftKey(entry) === key)
      ? opened.map((entry) => documentDraftKey(entry) === key ? ref : entry)
      : [...opened, ref];
  }
  return { activeBySession, openBySession, draftsByDocument };
}
