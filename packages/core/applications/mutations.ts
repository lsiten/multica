import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { applicationKeys } from "./queries";
import type { ApplicationOperationRequest, CreateApplicationRequest, UpdateApplicationRequest } from "./schema";

export function useApplicationOperation(workspaceId: string, id: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: ApplicationOperationRequest) => api.enqueueApplicationOperation(workspaceId, id, input),
    onSuccess: (operation) => client.setQueryData(applicationKeys.operation(workspaceId, id, operation.id), operation),
    onSettled: () => client.invalidateQueries({ queryKey: applicationKeys.all(workspaceId) }),
  });
}

export function useLaunchApplication(workspaceId: string, id: string) {
  return useMutation({ mutationFn: (endpointId: string) => api.launchApplication(workspaceId, id, endpointId) });
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
