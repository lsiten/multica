import { localReviewRequestSchema, localReviewDirectResponseSchema, localReviewRuntimeBindingSchema, type LocalReviewRequest } from "@multica/core/types/local-review";
import { ownsReviewRuntime } from "../shared/local-review-routing";

type Profile = { readonly name: string; readonly port: number };
export interface LocalReviewTransport {
  resolveProfile(): Promise<Profile | null>;
  health(profile: Profile): Promise<unknown>;
  review(profile: Profile, request: LocalReviewRequest): Promise<unknown>;
  discoverRuntime?(profile: Profile, request: LocalReviewRequest): Promise<unknown>;
}

// Keep one selected profile throughout the health check and the authenticated
// request. Re-resolving it between awaits could send a checked request elsewhere.
export async function requestLocalReview(input: unknown, transport: LocalReviewTransport) {
  const request = localReviewRequestSchema.parse(input);
  const profile = await transport.resolveProfile();
  if (!profile) return null;
  let runtimeId = request.runtime_id;
  if (!runtimeId && transport.discoverRuntime) {
    const binding = localReviewRuntimeBindingSchema.parse(await transport.discoverRuntime(profile, request));
    if (binding.task_id !== request.task_id || binding.workspace_id !== request.workspace_id) throw new Error("Legacy runtime discovery returned another task");
    runtimeId = binding.runtime_id;
  }
  const scopedRequest = { ...request, runtime_id: runtimeId };
  if (!ownsReviewRuntime(await transport.health(profile), scopedRequest, profile.name)) return null;
  return { ...localReviewDirectResponseSchema.parse(await transport.review(profile, scopedRequest)), runtime_id: runtimeId };
}
