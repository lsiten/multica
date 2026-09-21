import { z } from "zod";

export const VoiceTranscriptSchema = z.object({ text: z.string().trim().min(1).max(16 * 1024) });

export async function voiceAudioPayload(recording: Blob, signal: AbortSignal) {
  signal.throwIfAborted();
  if (!recording.size || recording.size > 512 * 1024) throw new Error("invalid_audio");
  const bytes = new Uint8Array(await recording.arrayBuffer());
  signal.throwIfAborted();
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return { mime_type: recording.type || "audio/webm", audio_base64: btoa(binary) };
}
