// @vitest-environment node
import { expect, it } from "vitest";
import { selectApplicationBoard, type ApplicationFilters } from "./filters";
import { applicationBoardSchema, applicationSchema, applicationInstanceSchema, defaultApplicationConfig } from "./schema";

const projectId = "11111111-1111-4111-8111-111111111111";
const secondProjectId = "22222222-2222-4222-8222-222222222222";
const runtimeId = "33333333-3333-4333-8333-333333333333";
const otherRuntimeId = "44444444-4444-4444-8444-444444444444";
const service = applicationSchema.parse({ id: projectId, workspace_id: projectId, project_id: projectId, created_by: projectId, name: "API", description: "Customer backend", kind: "service", revision: 1, config: defaultApplicationConfig(), created_at: "", updated_at: "" });
const composition = { ...service, id: secondProjectId, name: "Stack", description: "", kind: "composition" as const, relations: [{ source_id: secondProjectId, target_id: service.id, type: "contains" as const, required: true, condition: "" as const, start_external: false }] };
const healthy = applicationInstanceSchema.parse({ id: runtimeId, workspace_id: projectId, application_id: service.id, runtime_id: runtimeId, revision: 1, generation: 1, desired_state: "running", process_state: "running", health_state: "healthy", runtime_state: "online", status: "running" });
const failed = { ...healthy, id: otherRuntimeId, runtime_id: otherRuntimeId, status: "unhealthy" as const, health_state: "unhealthy" as const };
const board = applicationBoardSchema.parse({ applications: [service, composition, { ...service, id: otherRuntimeId, project_id: secondProjectId, name: "Idle" }], instances: [healthy, failed], endpoints: [{ id: projectId, application_id: service.id, instance_id: healthy.id, port: 8080, state: "published", visibility: "workspace", revision: 1 }], operations: [], summary: { services: 2, compositions: 1, instances: 2, running: 1, unhealthy: 1, offline: 0, stopped: 0, runtimes: 2 } });
const filters: ApplicationFilters = { projectId: "all", runtimeIds: null, search: "", status: "all", kind: "all", publication: "all" };

it("filters instances of a composition without dropping its member definitions", () => {
  const result = selectApplicationBoard(board, { ...filters, kind: "composition", runtimeIds: [otherRuntimeId], status: "attention" });
  expect(result.applications.map((application) => application.id)).toEqual([composition.id]);
  expect(result.instances.map((instance) => instance.id)).toEqual([failed.id]);
  expect(result.summary).toEqual({ services: 1, compositions: 1, running: 0, unhealthy: 1, offline: 0, runtimes: 1 });
});

it("keeps summary scope independent of selected status and type without counting shared members twice", () => {
  const result = selectApplicationBoard(board, { ...filters, projectId, status: "unhealthy", kind: "service" });
  expect(result.instances.map((instance) => instance.id)).toEqual([failed.id]);
  expect(result.summary).toEqual({ services: 1, compositions: 1, running: 1, unhealthy: 1, offline: 0, runtimes: 2 });
});

it("searches descriptions and limits publication to the matching instance", () => {
  const result = selectApplicationBoard(board, { ...filters, search: " CUSTOMER ", publication: "published" });
  expect(result.applications.map((application) => application.id)).toEqual([service.id]);
  expect(result.instances.map((instance) => instance.id)).toEqual([healthy.id]);
});

it("shows never-started services as stopped and returns no matches for an empty runtime", () => {
  expect(selectApplicationBoard(board, { ...filters, status: "stopped" }).applications.map((application) => application.name)).toEqual(["Idle"]);
  expect(selectApplicationBoard(board, { ...filters, runtimeIds: [projectId] }).applications).toEqual([]);
});
