import { z } from "zod";

const authorizationWireSchema = z.object({
  type: z.literal("mirror-authorization:request"),
  request_id: z.string().min(1).max(128),
  kind: z.enum(["system", "cli"]),
  title: z.string().min(1).max(160),
  message: z.string().min(1).max(2048),
  expires_at: z.string().datetime({ offset: true }),
});

const voiceTranscriptWireSchema = z.object({
  type: z.literal("mirror-voice:transcript"),
  seq: z.number().int().positive().safe(),
  text: z.string().min(1).max(16 * 1024),
});

const voiceErrorWireSchema = z.object({
  type: z.literal("mirror-voice:error"),
  seq: z.number().int().nonnegative().safe(),
  reason: z.enum([
    "invalid_audio",
    "denied",
    "transcriber_unavailable",
    "transcription_failed",
  ]),
});

export type MirrorAuthorizationWireMessage = z.infer<typeof authorizationWireSchema>;
export type MirrorVoiceWireMessage =
  | z.infer<typeof voiceTranscriptWireSchema>
  | z.infer<typeof voiceErrorWireSchema>;

function parseJson(value: unknown): unknown {
  if (typeof value !== "string" || value.length > 64 * 1024) return null;
  try {
    return JSON.parse(value) as unknown;
  } catch {
    return null;
  }
}

export function parseMirrorAuthorizationRequest(
  value: unknown,
  now = Date.now(),
): MirrorAuthorizationWireMessage | null {
  const parsed = authorizationWireSchema.safeParse(parseJson(value));
  if (!parsed.success) return null;
  const expiresAt = Date.parse(parsed.data.expires_at);
  if (!Number.isFinite(expiresAt) || expiresAt <= now) return null;
  return parsed.data;
}

export function parseMirrorVoiceMessage(value: unknown): MirrorVoiceWireMessage | null {
  const parsed = parseJson(value);
  const transcript = voiceTranscriptWireSchema.safeParse(parsed);
  if (transcript.success) return transcript.data;
  const error = voiceErrorWireSchema.safeParse(parsed);
  return error.success ? error.data : null;
}
