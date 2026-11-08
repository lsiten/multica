"use client";

import { useEffect, useMemo, useRef, type ReactNode } from "react";
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
  let audioBuffer: AudioBuffer;
  try {
    audioBuffer = await audioContext.decodeAudioData(bytes);
  } finally {
    await audioContext.close();
  }
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
  let unsubscribe: (() => void) | null = null;
  let subscriptionGeneration = 0;
  const getSnapshot = (): LocalVoiceStatus => status;
  const subscribe = (listener: () => void): (() => void) => {
    listeners.add(listener);
    if (listeners.size === 1) {
      const generation = ++subscriptionGeneration;
      const applyCurrent = (next: LocalVoiceStatus) => {
        if (generation === subscriptionGeneration) apply(next);
      };
      unsubscribe = api.onStatus(applyCurrent);
      void api.getStatus().then(applyCurrent, () => {});
    }
    return () => {
      listeners.delete(listener);
      if (listeners.size === 0) {
        subscriptionGeneration += 1;
        unsubscribe?.();
        unsubscribe = null;
      }
    };
  };
  const retry = (): void => {
    void api.retry();
  };
  const transcribe = async (recording: Blob, signal: AbortSignal): Promise<string> => {
    if (signal.aborted) return "";
    const id = `voice-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
    const onAbort = () => api.cancel(id);
    signal.addEventListener("abort", onAbort, { once: true });
    try {
      const samples = await decodeRecordingToPcm16(recording);
      if (signal.aborted || samples.length === 0) return "";
      const text = await api.transcribe(id, samples, getLanguage());
      return signal.aborted ? "" : text;
    } catch (error) {
      if (signal.aborted) return "";
      throw error;
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
  const language = useRef(mapVoiceLanguage(locale));
  useEffect(() => {
    language.current = mapVoiceLanguage(locale);
  }, [locale]);
  const adapter = useMemo(() => {
    const api = window.localVoiceAPI;
    if (!api) return null;
    return buildLocalVoiceAdapter(api, () => language.current);
  }, []);
  return <LocalVoiceContext.Provider value={adapter}>{children}</LocalVoiceContext.Provider>;
}
