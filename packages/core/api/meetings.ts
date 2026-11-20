import { z } from "zod";

const MeetingStatusSchema = z.enum([
  "recording",
  "paused",
  "finalizing",
  "organizing",
  "ready",
  "failed",
  "cancelled",
]);

const MeetingSourceTypeSchema = z.enum(["workspace", "chat", "group"]);

export const MeetingSegmentSchema = z.object({
  id: z.string().min(1),
  sequence: z.number().int().nonnegative(),
  start_ms: z.number().int().nonnegative(),
  end_ms: z.number().int().nonnegative(),
  speaker_label: z.string().default(""),
  text: z.string(),
  is_final: z.boolean().default(true),
  confidence: z.number().min(0).max(1).optional(),
});

export const MeetingSchema = z.object({
  id: z.string().min(1),
  workspace_id: z.string().min(1),
  creator_id: z.string().min(1),
  source_type: MeetingSourceTypeSchema,
  source_chat_session_id: z.string().min(1).optional(),
  source_agent_id: z.string().min(1).optional(),
  runtime_id: z.string().min(1).optional(),
  status: MeetingStatusSchema,
  language: z.string().default("zh"),
  title: z.string().default(""),
  timezone: z.string().default("Asia/Shanghai"),
  started_at: z.string(),
  ended_at: z.string().optional(),
  audio_retention: z.enum(["discard", "retain"]).default("discard"),
  project_id: z.string().min(1).optional(),
  topic: z.string().default(""),
  transcript: z.string().optional(),
  summary: z.string().optional(),
  decisions: z.array(z.string()).default([]),
  action_items: z.array(z.string()).default([]),
  classification: z.record(z.string(), z.unknown()).default({}),
  failure_reason: z.string().optional(),
  archived_at: z.string().optional(),
  created_at: z.string(),
  updated_at: z.string(),
  segments: z.array(MeetingSegmentSchema).optional(),
});

export const MeetingListSchema = z.array(MeetingSchema);
export type Meeting = z.infer<typeof MeetingSchema>;
export type MeetingSegment = z.infer<typeof MeetingSegmentSchema>;
