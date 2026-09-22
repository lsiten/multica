import type { LocalVoiceStatus } from "@multica/core/platform";
export const LOCAL_VOICE_CHANNEL = {
  status: "local-voice:status", changed: "local-voice:changed", retry: "local-voice:retry",
  transcribe: "local-voice:transcribe", cancel: "local-voice:cancel",
} as const;
export interface LocalVoiceAPI {
  readonly getStatus: () => Promise<LocalVoiceStatus>;
  readonly onStatus: (listener: (status: LocalVoiceStatus) => void) => () => void;
  readonly retry: () => Promise<void>;
  readonly transcribe: (id: string, samples: Float32Array) => Promise<string>;
  readonly cancel: (id: string) => void;
}
export function validVoiceSamples(value: unknown): value is Float32Array {
  return value instanceof Float32Array && value.length > 0 && value.length <= 16_000 * 61
    && value.every((sample) => Number.isFinite(sample) && Math.abs(sample) <= 1);
}
