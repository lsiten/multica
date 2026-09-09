import { localReviewBranchesRequestSchema, localReviewBranchesSchema, localReviewRequestSchema, localReviewDirectResponseSchema, localReviewRuntimeBindingSchema, type LocalReviewRequest } from "@multica/core/types/local-review";
import { ownsReviewRuntime } from "../shared/local-review-routing";
import { pagedReviewCapabilitySchema, pagedReviewRequestSchema, parsePagedReviewResponse, type PagedReviewRequest } from "@multica/core/types/local-review-pages";

type ReviewOperationRequest = LocalReviewRequest | PagedReviewRequest;

type Profile = { readonly name: string; readonly port: number };
export interface LocalReviewTransport {
  resolveProfile(): Promise<Profile | null>;
  health(profile: Profile, signal?: AbortSignal): Promise<unknown>;
  review(profile: Profile, request: ReviewOperationRequest, signal?: AbortSignal): Promise<unknown>;
  discoverRuntime?(profile: Profile, request: ReviewOperationRequest, signal?: AbortSignal): Promise<unknown>;
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
  if (paged && pagedReviewCapabilitySchema.parse(health).local_review_paging_supported !== true) throw new Error("local_review_paging_upgrade_required");
  return { response: await transport.review(profile, scopedRequest, signal), runtimeId };
}
