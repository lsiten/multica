export type LocalVoiceStatus =
  | { readonly phase: "downloading"; readonly percent: number }
  | { readonly phase: "loading"; readonly percent: 100 }
  | { readonly phase: "ready" }
  | { readonly phase: "failed"; readonly percent: number };

export interface LocalVoiceAdapter {
  readonly getSnapshot: () => LocalVoiceStatus;
  readonly subscribe: (listener: () => void) => () => void;
  readonly retry: () => void;
  readonly transcribe: (recording: Blob, signal: AbortSignal) => Promise<string>;
}
