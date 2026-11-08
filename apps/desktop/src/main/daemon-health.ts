import { z } from "zod";
import { parseWithFallback } from "@multica/core/api/schema";

const queueMetric = z.number().int().nonnegative().safe().catch(0);
const optionalText = z.string().optional().catch(undefined);

const daemonHealthSchema = z.object({
  status: optionalText,
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
