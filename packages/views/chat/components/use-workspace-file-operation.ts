import { useMutation, useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { ApiError } from "@multica/core/api";
import { useWorkspaceFileOperationStore, workspaceFileOperationScope, type PendingWorkspaceFileOperation } from "@multica/core/chat";
import { requestReviewPage } from "../../platform/local-review-pages";

const RejectedSchema = z.object({ code: z.enum(["workspace_file_operation_rejected", "workspace_file_unavailable", "workspace_file_directory_busy", "workspace_file_directory_changed"]) });

export function useWorkspaceFileOperation() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (pending: PendingWorkspaceFileOperation) => {
      const scope = workspaceFileOperationScope(pending.context);
      try {
        const result = await requestReviewPage({ ...pending.context, action: "file_operation", command_id: pending.commandId, file_operation: pending.operation });
        if (!("kind" in result) || result.kind !== "file_operation") throw new Error("File operation receipt expected");
        useWorkspaceFileOperationStore.getState().settle(scope, pending.commandId);
        return result;
      } catch (error) {
        if (error instanceof ApiError && RejectedSchema.safeParse(error.body).success) {
          useWorkspaceFileOperationStore.getState().settle(scope, pending.commandId);
        }
        throw error;
      }
    },
    retry: false,
    onSettled: (_result, _error, pending) => {
      for (const prefix of ["chat-workspace-files", "chat-workspace-file-content", "workspace-file-trash"]) void client.invalidateQueries({ queryKey: [prefix, pending.context.workspace_id] });
    },
  });
}
