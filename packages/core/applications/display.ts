import { z } from "zod";
import type { Application, ApplicationInstance } from "./schema";

export function parseApplicationArguments(value: string): string[] {
  if (!value.trim()) return [];
  return z.array(z.string()).max(127).parse(JSON.parse(value));
}

export function parseApplicationEnvironment(value: string): Record<string, string> {
  const result: Record<string, string> = {};
  for (const line of value.split(/\r?\n/)) {
    if (!line.trim()) continue;
    const separator = line.indexOf("=");
    const key = line.slice(0, separator).trim();
    if (separator < 1 || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key) || key.startsWith("MULTICA_") || Object.hasOwn(result, key)) throw new Error("Invalid application environment");
    result[key] = line.slice(separator + 1);
  }
  return result;
}

export function applicationEnvironmentText(values: Record<string, string>): string {
  return Object.entries(values).map(([key, value]) => `${key}=${value}`).join("\n");
}

export function applicationServiceIds(application: Application, applications: Application[]): Set<string> {
  const services = new Set<string>();
  const visited = new Set<string>();
  const byId = new Map(applications.map((app) => [app.id, app]));
  const visit = (app: Application) => {
    if (visited.has(app.id)) return;
    visited.add(app.id);
    if (app.kind === "service") { services.add(app.id); return; }
    for (const relation of app.relations) {
      if (relation.type !== "contains") continue;
      const child = byId.get(relation.target_id);
      if (child) visit(child);
    }
  };
  visit(application);
  return services;
}

export function applicationStatus(application: Application, applications: Application[], instances: ApplicationInstance[]): ApplicationInstance["status"] | "partial" {
  const ids = applicationServiceIds(application, applications);
  const relevant = instances.filter((instance) => ids.has(instance.application_id));
  if (!relevant.length) return "stopped";
  const statuses = relevant.map((instance) => instance.status);
  if (statuses.every((status) => status === "running") && [...ids].every((id) => relevant.some((instance) => instance.application_id === id))) return "running";
  if (statuses.some((status) => status === "running")) return "partial";
  for (const status of ["failed", "unhealthy", "starting", "stopping", "offline", "unknown"] as const) if (statuses.includes(status)) return status;
  return "stopped";
}

export function topLevelApplications(applications: Application[]): Application[] {
  const children = new Set(applications.flatMap((app) => app.relations.filter((relation) => relation.type === "contains").map((relation) => relation.target_id)));
  return applications.filter((app) => !children.has(app.id));
}
