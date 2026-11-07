"use client";

import { useMemo, type ReactNode } from "react";
import { LocalVoiceContext } from "@multica/views/platform";
import type { LocalVoiceAdapter, LocalVoiceStatus } from "@multica/core/platform";
import type { LocalVoiceAPI, LocalVoiceLanguage } from "../../../shared/local-voice";

// mapVoiceLanguage resolves the UI locale to one of the languages the offline
// whisper worker can transcribe. Unsupported locales fall back to English so a
// recording is never handed to the worker with an invalid language.
export function mapVoiceLanguage(locale: string): LocalVoiceLanguage {
  switch (locale) {
    case "ja":
      return "ja";
    case "ko":
      return "ko";
    case "fr":
      return "fr";
    case "zh-Hans":
    case "zh-Hant":
      return "zh";
    default:
      return "en";
  }
}

// decodeRecordingToPcm16 turns a recorded media Blob into mono 16 kHz PCM samples
// matching the offline whisper contract. Resampling through an OfflineAudioContext
// keeps the worker's sample-rate assumption correct regardless of the capture rate.
async function decodeRecordingToPcm16(blob: Blob): Promise<Float32Array> {
  const bytes = await blob.arrayBuffer();
  const audioContext = new AudioContext();
  const audioBuffer = await audioContext.decodeAudioData(bytes);
  void audioContext.close();
  const targetSampleRate = 16_000;
  const length = Math.max(1, Math.ceil(audioBuffer.duration * targetSampleRate));
  const offline = new OfflineAudioContext(1, length, targetSampleRate);
  const source = offline.createBufferSource();
  source.buffer = audioBuffer;
  source.connect(offline.destination);
  source.start();
  const rendered = await offline.startRendering();
  return rendered.getChannelData(0);
}

// buildLocalVoiceAdapter bridges the preload LocalVoiceAPI (offline whisper in the
// main process) to the shared LocalVoiceAdapter contract consumed by chat voice
// input. Status is mirrored through the shared subscribe/getSnapshot contract so
// the "preparing" progress UI works; aborting a recording cancels the worker job.
export function buildLocalVoiceAdapter(
  api: LocalVoiceAPI,
  getLanguage: () => LocalVoiceLanguage,
): LocalVoiceAdapter {
  let status: LocalVoiceStatus = { phase: "downloading", percent: 0 };
  const listeners = new Set<() => void>();
  const apply = (next: LocalVoiceStatus) => {
    status = next;
    for (const listener of listeners) listener();
  };
  const unsubscribe = api.onStatus(apply);
  void api.getStatus().then(apply, () => {});
  const getSnapshot = (): LocalVoiceStatus => status;
  const subscribe = (listener: () => void): (() => void) => {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
      if (listeners.size === 0) unsubscribe();
    };
  };
  const retry = (): void => {
    void api.retry();
  };
  const transcribe = async (recording: Blob, signal: AbortSignal): Promise<string> => {
    const id = `voice-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
    const onAbort = () => api.cancel(id);
    signal.addEventListener("abort", onAbort, { once: true });
    try {
      const samples = await decodeRecordingToPcm16(recording);
      if (signal.aborted || samples.length === 0) return "";
      return await api.transcribe(id, samples, getLanguage());
    } finally {
      signal.removeEventListener("abort", onAbort);
    }
  };
  return { getSnapshot, subscribe, retry, transcribe };
}

// LocalVoiceProvider mounts the offline whisper adapter on the desktop, where the
// preload exposes window.localVoiceAPI. On any build without it the context stays
// null and chat voice input falls back to runtime transcription.
export function LocalVoiceProvider({ locale, children }: { locale: string; children: ReactNode }) {
  const adapter = useMemo(() => {
    const api = window.localVoiceAPI;
    if (!api) return null;
    return buildLocalVoiceAdapter(api, () => mapVoiceLanguage(locale));
  }, [locale]);
  return <LocalVoiceContext.Provider value={adapter}>{children}</LocalVoiceContext.Provider>;
}
