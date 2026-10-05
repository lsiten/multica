import { z } from "zod";

export const HumanRequestAnswerSchema = z.object({
  revision: z.number().int().positive(),
  decision: z.enum(["approve", "reject", "choice", "input", "completed"]),
  answer: z.string().optional(),
});

export const HumanRequestSchema = z.object({
  id: z.string().uuid(),
  workspace_id: z.string().uuid(),
  source_task_id: z.string().uuid(),
  agent_id: z.string().uuid(),
  recipient_id: z.string().uuid(),
  issue_id: z.string().nullable().default(null),
  chat_session_id: z.string().nullable().default(null),
  project_id: z.string().nullable().default(null),
  revision: z.number().int().positive(),
  status: z.enum(["pending", "answered", "declined", "cancelled", "expired", "unknown"]).catch("unknown"),
  payload: z.object({
    key: z.string(),
    kind: z.enum(["confirmation", "choice", "input", "manual", "unknown"]).catch("unknown"),
    title: z.string().min(1),
    steps: z.array(z.string()).default([]),
    action_label: z.string().min(1),
    next: z.string().min(1),
    impact: z.string().optional(),
    choices: z.array(z.object({ id: z.string(), label: z.string(), recommended: z.boolean().optional() })).default([]),
    input_label: z.string().optional(),
    verification: z.string().optional(),
    details: z.string().optional(),
    response_mode: z.enum(["card_only", "chat_or_card"]).catch("card_only").optional().default("card_only"),
  }),
  response: HumanRequestAnswerSchema.nullable().optional().default(null),
  response_task_id: z.string().nullable().default(null),
  can_respond: z.boolean().catch(false).default(false),
  expires_at: z.string().datetime({ offset: true }),
}).superRefine((request, context) => {
  const payload = request.payload;
  if (payload.kind === "manual" && (!payload.steps.length || !payload.verification?.trim()) || payload.kind === "input" && !payload.input_label?.trim() || payload.kind === "choice" && (payload.choices.length < 2 || new Set(payload.choices.map(choice => choice.id)).size !== payload.choices.length)) {
    context.addIssue({ code: "custom", path: ["payload"], message: "Request is missing its actionable fields" });
  }
});

export const HumanRequestListSchema = z.array(HumanRequestSchema);

export const HumanTextReplyResultSchema = z.object({
  request: HumanRequestSchema,
  reply: z.object({ channel: z.string(), text: z.string(), reply_id: z.string().optional(), created_at: z.string().optional() }).nullable(),
  task_id: z.string().nullable(),
});
