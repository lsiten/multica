import { z } from "zod";

const identifier = z.string().uuid();

export const applicationConfigSchema = z.object({
  mode: z.enum(["managed", "external", "unknown"]).catch("unknown"),
  resource_id: z.string().default(""),
  ref: z.string().default(""),
  work_dir: z.string().default(""),
  command: z.array(z.string()).default([]),
  prepare: z.array(z.object({ args: z.array(z.string()), timeout_seconds: z.number().int().positive() })).default([]),
  environment: z.record(z.string(), z.string()).default({}),
  local_env: z.record(z.string(), z.string()).default({}),
  connections: z.array(z.object({ target_id: identifier, url_variable: z.string().min(1) })).default([]),
  port: z.number().int().min(0).max(65535).default(0),
  health: z.object({
    kind: z.enum(["none", "tcp", "http", "unknown"]).catch("unknown"),
    path: z.string().default(""),
    timeout_seconds: z.number().int().nonnegative().default(60),
    interval_seconds: z.number().int().positive().default(5),
  }),
  restart: z.object({
    enabled: z.boolean().default(false),
    max_attempts: z.number().int().nonnegative().default(3),
    delay_seconds: z.number().int().positive().default(5),
    restore: z.boolean().default(false),
  }),
  auto_publish: z.boolean().default(false),
  entry_path: z.string().default("/"),
});

export const applicationRelationSchema = z.object({
  source_id: identifier,
  target_id: identifier,
  type: z.enum(["contains", "depends_on", "related", "unknown"]).catch("unknown"),
  required: z.boolean().default(true),
  condition: z.enum(["", "started", "healthy", "unknown"]).catch("unknown").default(""),
  start_external: z.boolean().default(false),
});

export const applicationSchema = z.object({
  id: identifier,
  workspace_id: identifier,
  project_id: identifier,
  name: z.string().min(1),
  description: z.string().default(""),
  kind: z.enum(["service", "composition", "unknown"]).catch("unknown"),
  revision: z.number().int().positive(),
  created_by: identifier,
  created_at: z.string(),
  updated_at: z.string(),
  config: applicationConfigSchema,
  relations: z.array(applicationRelationSchema).default([]),
});

export const applicationListSchema = z.object({
  applications: z.array(applicationSchema),
  total: z.number().int().nonnegative(),
});

export const applicationPlanSchema = z.object({
  root_id: identifier,
  nodes: z.array(z.object({
    id: identifier,
    revision: z.number().int().positive(),
    required: z.boolean(),
    dependencies: z.array(z.object({ id: identifier, condition: z.enum(["healthy", "started", "unknown"]).catch("unknown") })),
  })),
  waves: z.array(z.array(identifier)),
});

export const applicationInstanceSchema = z.object({
  id: identifier, workspace_id: identifier, application_id: identifier, runtime_id: identifier,
  revision: z.number().int().positive(), observed_revision: z.number().int().nonnegative().default(0),
  generation: z.number().int().nonnegative(), observed_generation: z.number().int().nonnegative().default(0),
  desired_state: z.enum(["running", "stopped", "unknown"]).catch("unknown"),
  process_state: z.enum(["preparing", "starting", "running", "stopping", "stopped", "failed", "unknown"]).catch("unknown"),
  health_state: z.enum(["checking", "healthy", "unhealthy", "none", "unknown"]).catch("unknown"),
  runtime_state: z.enum(["online", "offline"]).catch("offline"),
  status: z.enum(["starting", "running", "stopping", "stopped", "failed", "unhealthy", "offline", "unknown"]).catch("unknown"),
  error: z.string().default(""), code_version: z.string().default(""), dirty: z.boolean().default(false),
  started_at: z.string().nullable().default(null), observed_at: z.string().nullable().default(null),
  metrics: z.record(z.string(), z.number()).default({}), can_manage: z.boolean().default(false),
});

export const applicationEndpointSchema = z.object({
  id: identifier, application_id: identifier, instance_id: identifier,
  port: z.number().int().positive().max(65535), entry_path: z.string().default("/"),
  visibility: z.enum(["workspace", "private", "unknown"]).catch("unknown"),
  state: z.enum(["published", "unpublished", "unknown"]).catch("unknown"),
  revision: z.number().int().positive(), can_manage: z.boolean().default(false),
});

export const applicationLaunchSchema = z.object({ url: z.string().url() });
export const applicationLogSchema = z.object({ text: z.string(), cursor: z.string(), gap: z.boolean() });
export type ApplicationLog = z.infer<typeof applicationLogSchema>;

export const applicationOperationSchema = z.object({
  id: identifier, workspace_id: identifier, application_id: identifier,
  action: z.enum(["start", "stop", "restart", "publish", "unpublish", "unknown"]).catch("unknown"),
  actor_type: z.enum(["member", "agent", "unknown"]).catch("unknown"), actor_id: identifier,
  state: z.enum(["queued", "running", "cancelling", "completed", "partial", "failed", "cancelled", "unknown"]).catch("unknown"),
  error: z.string().default(""), created_at: z.string(), completed_at: z.string().nullable().default(null),
  deadline_at: z.string().datetime({ offset: true }).nullable().catch(null).optional(),
  cancel_requested_at: z.string().nullable().default(null), cancel_actor_type: z.enum(["member", "agent", "unknown"]).catch("unknown").default("unknown"), cancel_actor_id: z.string().default(""),
  snapshot: z.object({ root_runtime_id: identifier, root_revision: z.number().int().positive(), plan: applicationPlanSchema, placements: z.record(identifier, identifier) }),
  steps: z.array(z.object({
    id: identifier, instance_id: identifier, application_id: identifier, runtime_id: identifier,
    generation: z.number().int().nonnegative(), wave: z.number().int(), required: z.boolean(),
    action: z.string(), state: z.enum(["queued", "running", "completed", "failed", "blocked", "cancelled", "unknown"]).catch("unknown"), error: z.string().default(""),
  })).default([]),
});

export const applicationBoardSchema = z.object({
  applications: z.array(applicationSchema), instances: z.array(applicationInstanceSchema),
  endpoints: z.array(applicationEndpointSchema), operations: z.array(applicationOperationSchema),
  summary: z.object({
    services: z.number().int().nonnegative(), compositions: z.number().int().nonnegative(),
    instances: z.number().int().nonnegative(), running: z.number().int().nonnegative(), unhealthy: z.number().int().nonnegative(),
    offline: z.number().int().nonnegative(), stopped: z.number().int().nonnegative(), runtimes: z.number().int().nonnegative(),
  }),
});

export type ApplicationInstance = z.infer<typeof applicationInstanceSchema>;
export type ApplicationEndpoint = z.infer<typeof applicationEndpointSchema>;
export type ApplicationOperation = z.infer<typeof applicationOperationSchema>;
export type ApplicationBoard = z.infer<typeof applicationBoardSchema>;

export interface ApplicationOperationRequest {
  action: "start" | "stop" | "restart" | "publish" | "unpublish";
  revision: number;
  runtime_id: string;
  placements?: Record<string, string>;
  idempotency_key: string;
  force?: boolean;
}

export type ApplicationConfig = z.infer<typeof applicationConfigSchema>;
export type ApplicationRelation = z.infer<typeof applicationRelationSchema>;
export type Application = z.infer<typeof applicationSchema>;
export type ApplicationList = z.infer<typeof applicationListSchema>;
export type ApplicationPlan = z.infer<typeof applicationPlanSchema>;

export interface CreateApplicationRequest {
  project_id: string;
  name: string;
  description?: string;
  kind: "service" | "composition";
  config: ApplicationConfig;
}

export interface UpdateApplicationRequest {
  revision: number;
  name?: string;
  description?: string;
  config?: ApplicationConfig;
  relations?: ApplicationRelation[];
}

export function defaultApplicationConfig(): ApplicationConfig {
  return {
    mode: "managed", resource_id: "", ref: "", work_dir: "", command: [], prepare: [],
    environment: {}, local_env: {}, connections: [], port: 0,
    health: { kind: "tcp", path: "", timeout_seconds: 60, interval_seconds: 5 },
    restart: { enabled: false, max_attempts: 3, delay_seconds: 5, restore: false },
    auto_publish: false, entry_path: "/",
  };
}
