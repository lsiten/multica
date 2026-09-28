import type { LocalVoiceStatus } from "@multica/core/platform";
const LOCAL_VOICE_LANGUAGES = ["en", "zh", "ja", "ko", "fr"] as const;
export type LocalVoiceLanguage = typeof LOCAL_VOICE_LANGUAGES[number];
export const LOCAL_VOICE_CHANNEL = {
  status: "local-voice:status", changed: "local-voice:changed", retry: "local-voice:retry",
  transcribe: "local-voice:transcribe", cancel: "local-voice:cancel",
} as const;
export interface LocalVoiceAPI {
  readonly getStatus: () => Promise<LocalVoiceStatus>;
  readonly onStatus: (listener: (status: LocalVoiceStatus) => void) => () => void;
  readonly retry: () => Promise<void>;
  readonly transcribe: (id: string, samples: Float32Array, language: LocalVoiceLanguage) => Promise<string>;
  readonly cancel: (id: string) => void;
}
export function validVoiceLanguage(value: unknown): value is LocalVoiceLanguage {
  return LOCAL_VOICE_LANGUAGES.some((language) => language === value);
}
export function validVoiceSamples(value: unknown): value is Float32Array {
  return value instanceof Float32Array && value.length > 0 && value.length <= 16_000 * 61
    && value.every((sample) => Number.isFinite(sample) && Math.abs(sample) <= 1);
}
