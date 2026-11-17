import { documentDraftKey, useDocModeStore, useWorkspaceFileEditStore, type DocumentRef } from "@multica/core/chat";
import { ApiError } from "@multica/core/api";
import { z } from "zod";
import type { ApplyRuntimeFileEditRequest } from "@multica/core/api/runtime-file-edit";
import { applyReviewFileEdit } from "../../platform/local-review-pages";

type SaveOutcome =
  | { readonly kind: "conflict" }
  | { readonly kind: "saved" }
  | { readonly kind: "continued"; readonly content: string; readonly baseContent: string };
const RejectedSchema = z.object({ code: z.literal("workspace_file_operation_rejected") });

export async function saveWorkspaceFileDocument(input: {
  readonly document: DocumentRef;
  readonly request: Omit<ApplyRuntimeFileEditRequest, "command_id"> & { readonly action: "merge_edit"; readonly edit_base: string };
}): Promise<SaveOutcome> {
  const edits = useWorkspaceFileEditStore.getState();
  const pending = edits.prepare(input.document, input.request);
  let result: Awaited<ReturnType<typeof applyReviewFileEdit>>;
  try {
    result = await applyReviewFileEdit(pending.request);
  } catch (error) {
    if (error instanceof ApiError && RejectedSchema.safeParse(error.body).success) edits.settle(pending);
    throw error;
  }
  edits.settle(pending);
  if (result.conflict) return { kind: "conflict" };
  const state = useDocModeStore.getState();
  const current = state.draftsByDocument[documentDraftKey(input.document)];
  if (current?.kind === "workspace_file" && (current.content !== pending.request.edit_new || current.baseContent !== pending.request.edit_base)) {
    // The next delta starts from the submitted draft, retaining remote merge
    // changes when that newer local delta is merged against the workspace.
    const continued = { ...current, baseContent: pending.request.edit_new };
    state.setDocumentDraft(input.document, continued);
    return { kind: "continued", content: continued.content, baseContent: continued.baseContent };
  }
  state.clearDocumentDraft(input.document);
  return { kind: "saved" };
}
