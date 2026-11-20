import { z } from "zod";
import { applicationConfigSchema, applicationRelationSchema } from "./schema";

export const applicationRevisionSchema = z.object({
  workspace_id: z.string().uuid(),
  application_id: z.string().uuid(),
  revision: z.number().int().positive(),
  config: applicationConfigSchema,
  relations: z.array(applicationRelationSchema),
  actor_type: z.enum(["member", "agent", "unknown"]).catch("unknown"),
  actor_id: z.string().uuid(),
  created_at: z.string().datetime({ offset: true }),
});

export const applicationRevisionPageSchema = z.object({
  revisions: z.array(applicationRevisionSchema).max(25),
  next_before_revision: z.number().int().positive().nullable(),
});

export type ApplicationRevision = z.infer<typeof applicationRevisionSchema>;
export type ApplicationRevisionPage = z.infer<typeof applicationRevisionPageSchema>;

export interface ApplicationRevisionScope {
  readonly workspaceId: string;
  readonly applicationId: string;
}

export interface RestoreApplicationRevisionRequest {
  readonly revision: number;
  readonly target_revision: number;
}
