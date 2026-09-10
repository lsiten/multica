import { api } from "@multica/core/api";
import { createSafeId } from "@multica/core/utils";
import { isLocalIndexAction } from "@multica/core/types/local-review-index";
import { readWithCancellation } from "./local-review-cancellation";
import { isPagedReviewDecision, pagedReviewRequestSchema, parsePagedReviewResponse, type PagedReviewInput } from "@multica/core/types/local-review-pages";

export async function requestReviewPage(input: PagedReviewInput, signal?: AbortSignal) {
  signal?.throwIfAborted();
  const request = pagedReviewRequestSchema.parse(isPagedReviewDecision(input.action ?? "manifest")
    ? { ...input, command_id: input.command_id ?? createSafeId() } : input);
  if (typeof window !== "undefined") {
    const daemon: unknown = Reflect.get(window, "daemonAPI");
    if (daemon && typeof daemon === "object" && "readLocalReviewPage" in daemon && typeof daemon.readLocalReviewPage === "function") {
      const read = daemon.readLocalReviewPage.bind(daemon);
      const response = await readWithCancellation((id) => read(request, id), daemon, isPagedReviewDecision(request.action) ? undefined : signal);
      signal?.throwIfAborted();
      if (response !== null) return parsePagedReviewResponse(request, response);
    }
  }
  const index = isLocalIndexAction(request.action);
  const supported = await (index ? api.supportsLocalIndex(signal) : api.supportsPagedLocalMR(signal));
  signal?.throwIfAborted();
  if (!supported) throw new Error(index ? "local_review_index_upgrade_required" : "local_review_paging_upgrade_required");
  return api.executePagedLocalReview(request, signal);
}

export async function readReviewManifest(input: PagedReviewInput, signal?: AbortSignal) {
  const response = await requestReviewPage(input, signal);
  if (!("header" in response)) throw new Error("Review manifest response expected");
  return response;
}

export async function readLocalIndex(input: PagedReviewInput, signal?: AbortSignal) {
  const response = await requestReviewPage({ ...input, action: "index" }, signal);
  if (!("kind" in response) || response.kind !== "index") throw new Error("Index status response expected");
  return response;
}

export async function changeLocalIndex(input: PagedReviewInput & { action: "stage" | "unstage" | "commit" }) {
  const response = await requestReviewPage(input);
  if (!("kind" in response) || response.kind !== "index_result") throw new Error("Index operation response expected");
  return response;
}

export async function readReviewFile(input: PagedReviewInput, signal?: AbortSignal) {
  const response = await requestReviewPage({ ...input, action: "file" }, signal);
  if (!("preview" in response)) throw new Error("Review file response expected");
  return response;
}

export async function readReviewContext(input: PagedReviewInput, signal?: AbortSignal) {
  const response = await requestReviewPage({ ...input, action: "context" }, signal);
  if (!("preview" in response)) throw new Error("Review context response expected");
  return response;
}

export async function readReviewRepositories(input: PagedReviewInput, signal?: AbortSignal) {
  const response = await requestReviewPage({ ...input, action: "repositories" }, signal);
  if (!("repositories" in response)) throw new Error("Review repositories expected");
  return response;
}

export async function readReviewCommits(input: PagedReviewInput, signal?: AbortSignal) {
  const response = await requestReviewPage({ ...input, action: "commits" }, signal);
  if (!("commits" in response)) throw new Error("Review commits expected");
  return response;
}

export async function readReviewContent(input: PagedReviewInput, signal?: AbortSignal) {
  const response = await requestReviewPage({ ...input, action: "content" }, signal);
  if (!("content" in response)) throw new Error("Review content expected");
  return response;
}

export async function renewReviewLease(input: PagedReviewInput, signal?: AbortSignal) {
  const response = await requestReviewPage({ ...input, action: "lease" }, signal);
  if (!("expires_at" in response)) throw new Error("Review lease expected");
  return response;
}
