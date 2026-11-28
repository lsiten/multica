import { assertLocalReviewActionSupported } from "@multica/core/types/local-review-retirement";
import { api } from "@multica/core/api";
import { createSafeId } from "@multica/core/utils";
import { isLocalIndexAction } from "@multica/core/types/local-review-index";
import { readWithCancellation } from "./local-review-cancellation";
import { isPagedReviewDecision, pagedReviewRequestSchema, parsePagedReviewResponse, type PagedReviewInput } from "@multica/core/types/local-review-pages";
import { ApplyRuntimeFileEditRequestSchema, parseRuntimeFileEditResult, type ApplyRuntimeFileEditRequest } from "@multica/core/api/runtime-file-edit";
import { workspaceFileOperationsCapabilitySchema } from "@multica/core/types/workspace-file-operation";

export class RuntimeFileEditUnconfirmedError extends Error {
  constructor() { super("File edit result is unconfirmed"); this.name = "RuntimeFileEditUnconfirmedError"; }
}

export async function requestReviewPage(input: PagedReviewInput, signal?: AbortSignal) {
  assertLocalReviewActionSupported(input.action);
  signal?.throwIfAborted();
  const request = pagedReviewRequestSchema.parse(input.action !== "file_operation" && isPagedReviewDecision(input.action ?? "manifest")
    ? { ...input, command_id: input.command_id ?? createSafeId() } : input);
  // Filesystem reads also receive server-owned resource identities. Immutable
  // review snapshots and other owning-runtime reads may use local IPC.
  const serverFileAction = request.action === "file_operation" || request.action === "workspace_files" || request.action === "file_content" || request.action === "workspace_trash";
  if (!request.project_resource_id && !serverFileAction && typeof window !== "undefined") {
    const daemon: unknown = Reflect.get(window, "daemonAPI");
    if (daemon && typeof daemon === "object" && "readLocalReviewPage" in daemon && typeof daemon.readLocalReviewPage === "function") {
      const read = daemon.readLocalReviewPage.bind(daemon);
      const response = await readWithCancellation((id) => read(request, id), daemon, isPagedReviewDecision(request.action) ? undefined : signal);
      signal?.throwIfAborted();
      if (response !== null) return parsePagedReviewResponse(request, response);
    }
  }
  const index = isLocalIndexAction(request.action);
  const selected = request.action === "merge_selected";
  const fileOperation = request.action === "file_operation" || request.action === "workspace_trash";
  const selectedFile = request.action === "file_operation" && request.file_operation && "expected_file_id" in request.file_operation && !!request.file_operation.expected_file_id;
  const supported = await (fileOperation ? api.getConfig().then((config) => {
    const capability = workspaceFileOperationsCapabilitySchema.parse(config);
    return capability.workspace_file_operations_supported === true && (!selectedFile || capability.workspace_file_selection_supported === true);
  }) : selected ? api.supportsSelectedMerge() : index ? api.supportsLocalIndex(signal) : api.supportsPagedLocalMR(signal));
  signal?.throwIfAborted();
  if (!supported) throw new Error(fileOperation ? "workspace_file_operations_upgrade_required" : selected ? "local_review_selected_merge_upgrade_required" : index ? "local_review_index_upgrade_required" : "local_review_paging_upgrade_required");
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

export async function applyReviewFileEdit(input: ApplyRuntimeFileEditRequest, signal?: AbortSignal) {
  const request = ApplyRuntimeFileEditRequestSchema.parse(input);
  signal?.throwIfAborted();
  if (!request.file_id && typeof window !== "undefined") {
    const daemon: unknown = Reflect.get(window, "daemonAPI");
    if (daemon && typeof daemon === "object" && "applyFileEdit" in daemon && typeof daemon.applyFileEdit === "function") {
      const response = await daemon.applyFileEdit(request);
      signal?.throwIfAborted();
      const result = parseRuntimeFileEditResult(response);
      if (!result) throw new RuntimeFileEditUnconfirmedError();
      return result;
    }
  }
  const result = await api.applyRuntimeFileEdit(request, signal);
  if (!result || (request.file_id && (result.file_id !== request.file_id || result.command_id !== request.command_id || !result.operation_id))) throw new RuntimeFileEditUnconfirmedError();
  return result;
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

export async function readReviewVersions(input: PagedReviewInput, signal?: AbortSignal) {
  const response = await requestReviewPage({ ...input, action: "versions" }, signal);
  if (!("versions" in response)) throw new Error("Review versions expected");
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

export async function readGitBranches(input: PagedReviewInput, signal?: AbortSignal) {
  const response = await requestReviewPage({ ...input, scope: "checkout", action: "branches" }, signal);
  if (!("branches" in response)) throw new Error("Git branches response expected");
  return response;
}

export async function readGitManifest(input: PagedReviewInput, signal?: AbortSignal) {
  return readReviewManifest({ ...input, scope: "checkout", action: "git_manifest" }, signal);
}

export async function readGitHistory(input: PagedReviewInput, signal?: AbortSignal) {
  const response = await requestReviewPage({ ...input, scope: "checkout", action: "git_history" }, signal);
  if (!("commits" in response)) throw new Error("Git history response expected");
  return response;
}
