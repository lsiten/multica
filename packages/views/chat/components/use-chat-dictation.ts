import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { LocalVoiceAdapter } from "@multica/core/platform";
import type { DictationDraft } from "../../editor/dictation-draft";
import { recordDictation, type DictationRecording } from "./chat-dictation-recorder";

type DictationPhase = "idle" | "requesting" | "recording" | "transcribing";
type Capture = {
  controller: AbortController;
  draft: DictationDraft;
  stream?: MediaStream;
  recording?: DictationRecording;
  timer?: ReturnType<typeof setTimeout>;
  startedAt: number;
  stopping: boolean;
  transcript: string;
};

interface ChatDictationOptions {
  enabled: boolean;
  onBegin: () => DictationDraft | null;
  onActiveChange: (active: boolean) => void;
  onVoice: (blob: Blob, signal: AbortSignal) => Promise<string>;
  onRealtimeVoice?: LocalVoiceAdapter["transcribeStream"];
}

function releaseCapture(capture: Capture) {
  clearTimeout(capture.timer);
  capture.controller.abort();
  capture.recording?.stop();
  capture.stream?.getTracks().forEach((track) => track.stop());
}

export function useChatDictation(options: ChatDictationOptions) {
  const currentOptions = useRef(options);
  currentOptions.current = options;
  const capture = useRef<Capture | null>(null);
  const [phase, setPhase] = useState<DictationPhase>("idle");
  const [seconds, setSeconds] = useState(0);
  const [error, setError] = useState(false);

  const cancel = useCallback(() => {
    const current = capture.current;
    if (!current) return;
    capture.current = null;
    releaseCapture(current);
    current.draft.cancel();
    currentOptions.current.onActiveChange(false);
    setPhase("idle");
  }, []);

  const fail = useCallback((current: Capture) => {
    if (capture.current !== current) return;
    capture.current = null;
    releaseCapture(current);
    if (current.transcript.trim()) current.draft.finish();
    else current.draft.cancel();
    currentOptions.current.onActiveChange(false);
    setPhase("idle");
    setError(true);
  }, []);

  const stop = useCallback(() => {
    const current = capture.current;
    if (!current || current.stopping || !current.recording) return;
    current.stopping = true;
    clearTimeout(current.timer);
    setPhase("transcribing");
    current.timer = setTimeout(() => fail(current), 120_000);
    current.recording.stop();
    current.stream?.getTracks().forEach((track) => track.stop());
  }, [fail]);

  const start = useCallback(async () => {
    const config = currentOptions.current;
    if (!config.enabled || capture.current) return;
    setError(false);
    if (!navigator.mediaDevices?.getUserMedia ||
      (!config.onRealtimeVoice && typeof MediaRecorder === "undefined")) {
      setError(true);
      return;
    }
    const draft = config.onBegin();
    if (!draft) return;
    const current: Capture = {
      controller: new AbortController(), draft, startedAt: 0, stopping: false, transcript: "",
    };
    capture.current = current;
    config.onActiveChange(true);
    setSeconds(0);
    setPhase("requesting");
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      current.stream = stream;
      if (capture.current !== current) {
        stream.getTracks().forEach((track) => track.stop());
        return;
      }
      current.recording = config.onRealtimeVoice
        ? config.onRealtimeVoice(stream, current.controller.signal, (text) => {
          if (capture.current === current) {
            current.transcript = text;
            draft.update(text);
          }
        })
        : recordDictation(stream, current.controller.signal, config.onVoice);
      current.startedAt = Date.now();
      setPhase("recording");
      current.timer = setTimeout(stop, 60_000);
      void current.recording.promise.then((text) => {
        if (capture.current !== current) return;
        if (!text.trim()) { fail(current); return; }
        capture.current = null;
        releaseCapture(current);
        draft.update(text.trim());
        draft.finish();
        config.onActiveChange(false);
        setPhase("idle");
      }, () => fail(current));
    } catch {
      fail(current);
    }
  }, [fail, stop]);

  useLayoutEffect(() => {
    if (!options.enabled) cancel();
    return cancel;
  }, [options.enabled, cancel]);

  useEffect(() => {
    if (phase !== "recording") return;
    const timer = setInterval(() => {
      if (capture.current) setSeconds(Math.floor((Date.now() - capture.current.startedAt) / 1000));
    }, 250);
    return () => clearInterval(timer);
  }, [phase]);

  useEffect(() => {
    if (phase === "idle") return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      cancel();
    };
    const onVisibility = () => { if (document.hidden) cancel(); };
    window.addEventListener("keydown", onKey);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.removeEventListener("keydown", onKey);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [phase, cancel]);

  return { phase, seconds, error, start, stop, cancel, isActive: () => capture.current !== null };
}
