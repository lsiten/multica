import { z } from "zod";

export const selectedMergeCapabilitySchema = z.object({ local_review_selected_merge_supported: z.boolean().optional().catch(false) });
export const selectedMergeResultSchema = z.object({
  kind: z.literal("selected_merge"), version_id: z.string().regex(/^[a-f0-9]{64}$/), target: z.string().min(1),
  paths: z.array(z.string().min(1).max(4096)).min(1).max(10000),
  commit: z.union([z.literal(""), z.string().regex(/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/)]),
  conflicts: z.array(z.string().min(1).max(4096)).max(10000), runtime_id: z.string().optional(),
}).refine((result) => result.commit ? result.conflicts.length === 0 : result.conflicts.length > 0, "Commit or conflict result required");
export type SelectedMergeResult = z.infer<typeof selectedMergeResultSchema>;
