import { api } from "@multica/core/api";
import type { LocalReviewRequest } from "@multica/core/types/local-review";
import { localReviewRuntimeHealthSchema } from "@multica/core/types/local-review";
import { selectedMergeCapabilitySchema } from "@multica/core/types/local-review-selection";
import { requestReviewPage } from "./local-review-pages";

export async function supportsSelectedMerge(request: LocalReviewRequest): Promise<boolean> {
  if (typeof window !== "undefined") {
    const daemon: unknown = Reflect.get(window, "daemonAPI");
    if (daemon && typeof daemon === "object" && "reviewInventory" in daemon && typeof daemon.reviewInventory === "function") {
      const response: unknown = await daemon.reviewInventory();
      if (response && typeof response === "object" && "health" in response) {
        const health = localReviewRuntimeHealthSchema.parse(response.health);
        if (health.workspaces.some((workspace) => workspace.id === request.workspace_id && workspace.runtimes.includes(request.runtime_id || ""))) return selectedMergeCapabilitySchema.parse(response.health).local_review_selected_merge_supported === true;
      }
    }
  }
  return api.supportsSelectedMerge();
}

export async function mergeSelectedFiles(input: LocalReviewRequest & { version_id: string; paths: string[]; message: string }) {
  const response = await requestReviewPage({ ...input, action: "merge_selected", snapshot_id: input.version_id });
  if (!("kind" in response) || response.kind !== "selected_merge") throw new Error("Selected merge result expected");
  return response;
}
