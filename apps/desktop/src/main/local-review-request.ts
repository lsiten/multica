import { localReviewBranchesRequestSchema, localReviewBranchesSchema, localReviewRequestSchema, localReviewDirectResponseSchema, localReviewRuntimeBindingSchema, type LocalReviewRequest } from "@multica/core/types/local-review";
import { ownsReviewRuntime } from "../shared/local-review-routing";
import { selectedMergeCapabilitySchema } from "@multica/core/types/local-review-selection";
import { isLocalIndexAction, localIndexCapabilitySchema } from "@multica/core/types/local-review-index";
import { pagedReviewCapabilitySchema, pagedReviewRequestSchema, parsePagedReviewResponse, type PagedReviewRequest } from "@multica/core/types/local-review-pages";
import { ApplyRuntimeFileEditRequestSchema, type ApplyRuntimeFileEditRequest, parseRuntimeFileEditResult } from "@multica/core/api/runtime-file-edit";

type ReviewOperationRequest = LocalReviewRequest | PagedReviewRequest;

type Profile = { readonly name: string; readonly port: number };
export interface LocalReviewTransport {
  resolveProfile(): Promise<Profile | null>;
  health(profile: Profile, signal?: AbortSignal): Promise<unknown>;
  review(profile: Profile, request: ReviewOperationRequest | ApplyRuntimeFileEditRequest, signal?: AbortSignal): Promise<unknown>;
  discoverRuntime?(profile: Profile, request: ReviewOperationRequest | ApplyRuntimeFileEditRequest, signal?: AbortSignal): Promise<unknown>;
}

// Keep one selected profile throughout the health check and the authenticated
// request. Re-resolving it between awaits could send a checked request elsewhere.
export async function requestLocalReview(input: unknown, transport: LocalReviewTransport) {
  const result = await requestReviewOperation(localReviewRequestSchema.parse(input), transport);
  if (result === null) return null;
  return { ...localReviewDirectResponseSchema.parse(result.response), runtime_id: result.runtimeId };
}

export async function requestLocalReviewBranches(input: unknown, transport: LocalReviewTransport, signal?: AbortSignal) {
  const request = localReviewBranchesRequestSchema.parse(input);
  const result = await requestReviewOperation({ ...request, action: "branches" }, transport, { signal });
  return result === null ? null : localReviewBranchesSchema.parse(result.response);
}

export async function requestLocalReviewPage(input: unknown, transport: LocalReviewTransport, signal?: AbortSignal) {
  const request = pagedReviewRequestSchema.parse(input);
  const result = await requestReviewOperation(request, transport, { paged: true, signal });
  return result === null ? null : { ...parsePagedReviewResponse(request, result.response), runtime_id: result.runtimeId };
}

// The local (desktop) edit path mirrors requestLocalReview: resolve the
 // owning profile + runtime, verify this runtime owns the task, then relay
 // the edit to the daemon's review handler (which writes the working file).
 // The edit request's workspace_id / runtime_id are optional (the web path
 // omits them); here they scope the ownership check, so coerce to strings.
export async function requestLocalReviewFileEdit(input: unknown, transport: LocalReviewTransport, signal?: AbortSignal) {
  const request = ApplyRuntimeFileEditRequestSchema.parse(input);
  signal?.throwIfAborted();
  const profile = await transport.resolveProfile();
  signal?.throwIfAborted();
  if (!profile) return null;
  let runtimeId = request.runtime_id;
  if (!runtimeId && transport.discoverRuntime) {
    const binding = localReviewRuntimeBindingSchema.parse(await transport.discoverRuntime(profile, request, signal));
    signal?.throwIfAborted();
    if (binding.task_id !== request.task_id || binding.workspace_id !== request.workspace_id) throw new Error("Legacy runtime discovery returned another task");
    runtimeId = binding.runtime_id;
  }
  const scopedRequest = { ...request, runtime_id: runtimeId, workspace_id: request.workspace_id ?? "" };
  const health = await transport.health(profile, signal);
  signal?.throwIfAborted();
  if (!ownsReviewRuntime(health, { workspace_id: scopedRequest.workspace_id, runtime_id: scopedRequest.runtime_id ?? "" }, profile.name)) return null;
  const response = await transport.review(profile, scopedRequest, signal);
  return parseRuntimeFileEditResult(response);
}

async function requestReviewOperation(request: ReviewOperationRequest, transport: LocalReviewTransport, options: { readonly paged?: boolean; readonly signal?: AbortSignal } = {}) {
  const { signal, paged } = options;
  signal?.throwIfAborted();
  const profile = await transport.resolveProfile();
  signal?.throwIfAborted();
  if (!profile) return null;
  let runtimeId = request.runtime_id;
  if (!runtimeId && transport.discoverRuntime) {
    const binding = localReviewRuntimeBindingSchema.parse(await transport.discoverRuntime(profile, request, signal));
    signal?.throwIfAborted();
    if (binding.task_id !== request.task_id || binding.workspace_id !== request.workspace_id) throw new Error("Legacy runtime discovery returned another task");
    runtimeId = binding.runtime_id;
  }
  const scopedRequest = { ...request, runtime_id: runtimeId };
  const health = await transport.health(profile, signal);
  signal?.throwIfAborted();
  if (!ownsReviewRuntime(health, scopedRequest, profile.name)) return null;
  if (paged) {
    if (request.action === "merge_selected") {
      if (selectedMergeCapabilitySchema.parse(health).local_review_selected_merge_supported !== true) throw new Error("local_review_selected_merge_upgrade_required");
    } else if (isLocalIndexAction(request.action ?? "read")) {
      if (localIndexCapabilitySchema.parse(health).local_review_index_supported !== true) throw new Error("local_review_index_upgrade_required");
    } else if (pagedReviewCapabilitySchema.parse(health).local_review_paging_supported !== true) throw new Error("local_review_paging_upgrade_required");
  }
  return { response: await transport.review(profile, scopedRequest, signal), runtimeId };
}
