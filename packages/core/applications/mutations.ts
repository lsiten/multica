import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { applicationKeys } from "./queries";
import type { ApplicationOperationRequest, CreateApplicationRequest, UpdateApplicationRequest } from "./schema";
import { useApplicationOperationPanel } from "./operation-store";
import { createSafeId } from "../utils";

export interface ApplicationBulkOperationItem {
  readonly applicationId: string;
  readonly revision: number;
  readonly runtimeId: string;
}

export interface ApplicationBulkOperationResult {
  readonly succeeded: readonly string[];
  readonly failed: readonly { readonly item: ApplicationBulkOperationItem; readonly error: string }[];
}

export class ApplicationBulkOperationError extends Error {
  constructor(readonly result: ApplicationBulkOperationResult) {
    super("Some application operations failed");
    this.name = "ApplicationBulkOperationError";
  }
}

export function useApplicationBulkOperation(workspaceId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({ action, items, force = false }: { readonly action: ApplicationOperationRequest["action"]; readonly items: readonly ApplicationBulkOperationItem[]; readonly force?: boolean }): Promise<ApplicationBulkOperationResult> => {
      const results = await Promise.allSettled(items.map((item) => api.enqueueApplicationOperation(workspaceId, item.applicationId, { action, revision: item.revision, runtime_id: item.runtimeId, force, idempotency_key: createSafeId() })));
      const succeeded: string[] = [];
      const failed: { item: ApplicationBulkOperationItem; error: string }[] = [];
      results.forEach((result, index) => {
        const item = items[index];
        if (!item) return;
        if (result.status === "fulfilled") succeeded.push(result.value.id);
        else failed.push({ item, error: result.reason instanceof Error ? result.reason.message : "Operation failed" });
      });
      const summary = { succeeded, failed } satisfies ApplicationBulkOperationResult;
      if (!succeeded.length && failed.length) throw new ApplicationBulkOperationError(summary);
      return summary;
    },
    onSettled: () => client.invalidateQueries({ queryKey: applicationKeys.all(workspaceId) }),
  });
}

export function useApplicationOperation(workspaceId: string, id: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: ApplicationOperationRequest) => api.enqueueApplicationOperation(workspaceId, id, input),
    onSuccess: (operation) => {
      client.setQueryData(applicationKeys.operation(workspaceId, id, operation.id), operation);
      useApplicationOperationPanel.getState().focus(workspaceId, id, operation.id);
    },
    onSettled: (operation) => { if (operation?.local && operation.sync_pending) return; return client.invalidateQueries({ queryKey: applicationKeys.all(workspaceId) }); },
  });
}

export function useLaunchApplication(workspaceId: string, id: string) {
  return useMutation({ mutationFn: (endpointId: string) => api.launchApplication(workspaceId, id, endpointId) });
}

export function useCreateApplicationServiceAccess(workspaceId: string, id: string) {
  return useMutation({ mutationFn: (endpointId: string) => api.createApplicationServiceAccess(workspaceId, id, endpointId) });
}

export function useCancelApplicationOperation(workspaceId: string, id: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (operationId: string) => api.cancelApplicationOperation(workspaceId, id, operationId),
    onSuccess: (operation) => client.setQueryData(applicationKeys.operation(workspaceId, id, operation.id), operation),
    onSettled: () => client.invalidateQueries({ queryKey: applicationKeys.all(workspaceId) }),
  });
}

export function useCreateApplication(workspaceId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateApplicationRequest) => api.createApplication(workspaceId, input),
    onSuccess: (application) => client.setQueryData(applicationKeys.detail(workspaceId, application.id), application),
    onSettled: () => client.invalidateQueries({ queryKey: applicationKeys.all(workspaceId) }),
  });
}

export function useUpdateApplication(workspaceId: string, id: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: UpdateApplicationRequest) => api.updateApplication(workspaceId, id, input),
    onSuccess: (application) => client.setQueryData(applicationKeys.detail(workspaceId, id), application),
    onSettled: () => client.invalidateQueries({ queryKey: applicationKeys.all(workspaceId) }),
  });
}

export function useDeleteApplication(workspaceId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, revision }: { id: string; revision: number }) => api.deleteApplication(workspaceId, id, revision),
    onSuccess: (_result, { id }) => client.removeQueries({ queryKey: applicationKeys.detail(workspaceId, id) }),
    onSettled: () => client.invalidateQueries({ queryKey: applicationKeys.all(workspaceId) }),
  });
}
