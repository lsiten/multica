export class VoiceTranscriptionError extends Error {
  constructor(readonly code: "no_speech" | "unreliable_transcript") {
    super(code);
    this.name = "VoiceTranscriptionError";
  }
}
