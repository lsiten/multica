import { z } from "zod";
import { applicationServiceIds } from "./display";
import type { ApplicationBoard, ApplicationInstance } from "./schema";

export const applicationStatusFilterSchema = z.enum([
  "all", "attention", "starting", "running", "stopping", "stopped", "failed", "unhealthy", "offline", "unknown",
]).catch("all");
export const applicationKindFilterSchema = z.enum(["all", "service", "composition"]).catch("all");
export const applicationPublicationFilterSchema = z.enum(["all", "published", "unpublished"]).catch("all");

export interface ApplicationFilters {
  readonly projectId: string;
  readonly runtimeIds: readonly string[] | null;
  readonly search: string;
  readonly status: z.infer<typeof applicationStatusFilterSchema>;
  readonly kind: z.infer<typeof applicationKindFilterSchema>;
  readonly publication: z.infer<typeof applicationPublicationFilterSchema>;
}

function matchesStatus(instance: ApplicationInstance, status: ApplicationFilters["status"]) {
  if (status === "all") return true;
  if (status === "attention") return instance.status === "failed" || instance.status === "unhealthy";
  return instance.status === status;
}

/** Keep definitions and instances separate: a composition matches through its members. */
export function selectApplicationBoard(board: ApplicationBoard, filters: ApplicationFilters) {
  const search = filters.search.trim().toLocaleLowerCase();
  const scopedInstances = board.instances.filter((instance) => !filters.runtimeIds || filters.runtimeIds.includes(instance.runtime_id));
  const scopedApplications = board.applications.filter((application) => {
    if (filters.projectId !== "all" && application.project_id !== filters.projectId) return false;
    if (search && !`${application.name} ${application.description}`.toLocaleLowerCase().includes(search)) return false;
    const ids = applicationServiceIds(application, board.applications);
    return !filters.runtimeIds || scopedInstances.some((instance) => ids.has(instance.application_id));
  });
  const scopeIds = new Set(scopedApplications.flatMap((application) => [...applicationServiceIds(application, board.applications)]));
  const scopeInstances = scopedInstances.filter((instance) => scopeIds.has(instance.application_id));
  const publishedIds = new Set(board.endpoints.filter((endpoint) => endpoint.state === "published").map((endpoint) => endpoint.instance_id));
  const matchesPublication = (instance: ApplicationInstance) => filters.publication === "all" || publishedIds.has(instance.id) === (filters.publication === "published");
  const applications = scopedApplications.filter((application) => {
    if (filters.kind !== "all" && application.kind !== filters.kind) return false;
    const ids = applicationServiceIds(application, board.applications);
    const instances = scopeInstances.filter((instance) => ids.has(instance.application_id));
    if (!instances.length) return filters.publication === "all" && (filters.status === "all" || filters.status === "stopped");
    return instances.some((instance) => matchesStatus(instance, filters.status) && matchesPublication(instance));
  });
  const selectedIds = new Set(applications.flatMap((application) => [...applicationServiceIds(application, board.applications)]));
  const instances = scopeInstances.filter((instance) => selectedIds.has(instance.application_id) && matchesStatus(instance, filters.status) && matchesPublication(instance));
  return {
    applications,
    instances,
    scopedInstances: scopeInstances,
    summary: {
      services: scopedApplications.filter((application) => application.kind === "service").length,
      compositions: scopedApplications.filter((application) => application.kind === "composition").length,
      running: scopeInstances.filter((instance) => instance.status === "running").length,
      unhealthy: scopeInstances.filter((instance) => matchesStatus(instance, "attention")).length,
      offline: scopeInstances.filter((instance) => instance.status === "offline").length,
      runtimes: new Set(scopeInstances.map((instance) => instance.runtime_id)).size,
    },
  };
}
