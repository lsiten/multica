import { api } from "@multica/core/api";
import { createSafeId } from "@multica/core/utils";
import type { LocalMR, LocalReviewRequest, LocalReviewSnapshot } from "@multica/core/types/local-review";
import { localReviewBranchesSchema, localReviewDirectResponseSchema } from "@multica/core/types/local-review";
import { reconcileLocalReviewInventory } from "./local-review-inventory";
import { readWithCancellation } from "./local-review-cancellation";

export async function readLocalReview(request: LocalReviewRequest): Promise<LocalReviewSnapshot & { command_status?: string; command_error?: string; review_id?: string; history?: LocalMR["events"] }> {
  const operation = request.action && request.action !== "read"
    ? { ...request, command_id: request.command_id ?? createSafeId() }
    : request;
  if (typeof window !== "undefined") {
    const daemon: unknown = Reflect.get(window, "daemonAPI");
    if (daemon && typeof daemon === "object" && "readLocalReview" in daemon && typeof daemon.readLocalReview === "function") {
      const local: unknown = await daemon.readLocalReview(operation);
      if (local !== null) return localReviewDirectResponseSchema.parse(local);
    }
  }
  if (!await api.supportsLocalMR()) throw new Error("local_review_upgrade_required");
  return api.executeLocalReview(operation);
}

export async function localReviewInventory(issueId?: string) {
  const rows = await api.listReviewWorktrees(0, undefined, issueId);
  if (issueId) {
    let pageLength = rows.length;
    while (pageLength === 100) {
      const page = await api.listReviewWorktrees(rows.length, undefined, issueId);
      rows.push(...page);
      pageLength = page.length;
    }
  }
  return reconcileLocalReviewInventory(rows);
}

export async function localReviewInventoryPage(offset: number, agentId?: string) {
  return reconcileLocalReviewInventory(await api.listReviewWorktrees(offset, agentId));
}

export async function readLocalReviewBranches(request: LocalReviewRequest, signal?: AbortSignal): Promise<string[]> {
  signal?.throwIfAborted();
  if (typeof window !== "undefined") {
    const daemon: unknown = Reflect.get(window, "daemonAPI");
    if (daemon && typeof daemon === "object" && "readLocalReviewBranches" in daemon && typeof daemon.readLocalReviewBranches === "function") {
      const read = daemon.readLocalReviewBranches.bind(daemon);
      const local = await readWithCancellation((id) => read(request, id), daemon, signal);
      if (local !== null) return localReviewBranchesSchema.parse(local).branches;
    }
  }
  const supported = await api.supportsLocalMR(signal);
  signal?.throwIfAborted();
  if (!supported) throw new Error("local_review_upgrade_required");
  return api.listLocalReviewBranches(request, signal);
}
