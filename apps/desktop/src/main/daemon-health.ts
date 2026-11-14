import { z } from "zod";
import { parseWithFallback } from "@multica/core/api/schema";

const queueMetric = z.number().int().nonnegative().safe().catch(0);
const optionalText = z.string().optional().catch(undefined);
const requiredProcessServices = ["ai", "mirror", "application", "environment", "gateway"];
const processOwner = z.object({
  name: z.string(),
  configured: z.boolean(),
  state: z.string(),
  ready: z.boolean(),
  reason: optionalText,
});

const daemonHealthSchema = z.object({
  status: optionalText,
  owners_ready: z.boolean().optional().catch(undefined),
  process_owners: z.array(processOwner).catch([]),
  pid: z.number().int().positive().optional().catch(undefined),
  profile: optionalText,
  os: optionalText,
  uptime: optionalText,
  daemon_id: optionalText,
  device_name: optionalText,
  server_url: optionalText,
  cli_version: optionalText,
  active_task_count: z.number().int().nonnegative().optional().catch(undefined),
  agents: z.array(z.string()).catch([]),
  workspaces: z.array(z.unknown()).catch([]),
  // Missing on older daemons; the current daemon always owns all five roles.
  // Defaulting here keeps the desktop from rendering a healthy strict
  // multi-process daemon as if every service were disabled.
  process_services: z
    .array(z.string())
    .transform((services) =>
      requiredProcessServices.every((service) => services.includes(service))
        ? services
        : requiredProcessServices,
    )
    .default(requiredProcessServices)
    .catch(requiredProcessServices),
  pending_jev_decision_report_count: queueMetric,
  pending_jev_decision_report_bytes: queueMetric,
  failed_jev_decision_report_count: queueMetric,
  failed_jev_decision_report_bytes: queueMetric,
});

export type HealthPayload = z.infer<typeof daemonHealthSchema>;

export function parseDaemonHealth(value: unknown): HealthPayload | null {
  return parseWithFallback<HealthPayload | null>(value, daemonHealthSchema, null, {
    endpoint: "daemon/health",
    redactPayload: true,
  });
}
