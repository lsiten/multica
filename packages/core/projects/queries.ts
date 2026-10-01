import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { getCurrentSlug } from "../platform/workspace-storage";

export const projectKeys = {
  all: (wsId: string) => ["projects", wsId] as const,
  list: (wsId: string) => [...projectKeys.all(wsId), "list"] as const,
  detail: (wsId: string, id: string) =>
    [...projectKeys.all(wsId), "detail", id] as const,
};

export function projectListOptions(wsId: string) {
  return queryOptions({
    queryKey: projectKeys.list(wsId),
    queryFn: () => {
      const slug = getCurrentSlug();
      return slug ? api.listProjects(undefined, slug) : api.listProjects();
    },
    select: (data) => data.projects.filter((project) => project.workspace_id === wsId),
  });
}

export function projectDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: projectKeys.detail(wsId, id),
    queryFn: async () => {
      const project = await api.getProject(id, getCurrentSlug() ?? undefined);
      if (project.workspace_id !== wsId) {
        throw new Error("project response belongs to another workspace");
      }
      return project;
    },
  });
}
