import { api } from "@multica/core/api";
import { createSafeId } from "@multica/core/utils";
import type { LocalMR, LocalReviewRequest, LocalReviewSnapshot } from "@multica/core/types/local-review";
import { localReviewDirectResponseSchema } from "@multica/core/types/local-review";

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

export async function localReviewInventory() {
  return api.listReviewWorktrees();
}

export async function localReviewInventoryPage(offset: number, agentId?: string) {
  return api.listReviewWorktrees(offset, agentId);
}
