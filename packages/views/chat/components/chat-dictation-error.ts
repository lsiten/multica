export type DictationErrorKind = "permission" | "microphone" | "unsupported" | "empty" | "transcription";

export class DictationAudioError extends Error {
  constructor(readonly kind: "empty" | "recording") {
    super(kind === "empty" ? "Empty or oversized recording" : "Microphone recording failed");
    this.name = "DictationAudioError";
  }
}

export function dictationErrorKind(error: unknown): DictationErrorKind {
  if (error instanceof DOMException) {
    if (error.name === "NotAllowedError" || error.name === "SecurityError") return "permission";
    if (["NotFoundError", "NotReadableError", "AbortError"].includes(error.name)) return "microphone";
  }
  if (error instanceof DictationAudioError) return error.kind === "empty" ? "empty" : "microphone";
  return "transcription";
}
