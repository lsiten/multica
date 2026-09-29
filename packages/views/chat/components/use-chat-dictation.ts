import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { LocalVoiceAdapter } from "@multica/core/platform";
import type { DictationDraft } from "../../editor/dictation-draft";
import { recordDictation, type DictationRecording } from "./chat-dictation-recorder";
import { observeDictationVolume } from "./dictation-volume";

export type DictationPhase = "idle" | "requesting" | "recording" | "transcribing";
type Capture = {
  controller: AbortController;
  draft: DictationDraft;
  stream?: MediaStream;
  recording?: DictationRecording;
  timer?: ReturnType<typeof setTimeout>;
  startedAt: number;
  stopping: boolean;
  transcript: string;
  closeMeter?: () => void;
  completion: Promise<boolean>;
  complete: (success: boolean) => void;
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
  capture.closeMeter?.();
  capture.stream?.getTracks().forEach((track) => track.stop());
}

export function useChatDictation(options: ChatDictationOptions) {
  const currentOptions = useRef(options);
  currentOptions.current = options;
  const capture = useRef<Capture | null>(null);
  const [phase, setPhase] = useState<DictationPhase>("idle");
  const [seconds, setSeconds] = useState(0);
  const [level, setLevel] = useState(0);
  const [error, setError] = useState(false);
  const [hasTranscript, setHasTranscript] = useState(false);

  const cancel = useCallback(() => {
    const current = capture.current;
    if (!current) return;
    capture.current = null;
    releaseCapture(current);
    current.draft.cancel();
    current.complete(false);
    currentOptions.current.onActiveChange(false);
    setPhase("idle");
    setLevel(0);
  }, []);

  const fail = useCallback((current: Capture) => {
    if (capture.current !== current) return;
    capture.current = null;
    releaseCapture(current);
    if (current.transcript.trim()) current.draft.finish();
    else current.draft.cancel();
    currentOptions.current.onActiveChange(false);
    setPhase("idle");
    setLevel(0);
    setError(true);
    current.complete(false);
  }, []);

  const finish = useCallback((): Promise<boolean> => {
    const current = capture.current;
    if (!current?.recording) return Promise.resolve(false);
    if (current.stopping) return current.completion;
    current.stopping = true;
    clearTimeout(current.timer);
    current.closeMeter?.();
    setPhase("transcribing");
    current.timer = setTimeout(() => fail(current), 120_000);
    try {
      current.recording.stop();
      current.stream?.getTracks().forEach((track) => track.stop());
    } catch {
      fail(current);
    }
    return current.completion;
  }, [fail]);
  const stop = useCallback(() => { void finish(); }, [finish]);

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
    let resolveCompletion!: (success: boolean) => void;
    const completion = new Promise<boolean>((resolve) => { resolveCompletion = resolve; });
    const current: Capture = {
      controller: new AbortController(), draft, startedAt: 0, stopping: false, transcript: "",
      completion, complete: resolveCompletion,
    };
    capture.current = current;
    config.onActiveChange(true);
    setHasTranscript(false);
    setSeconds(0);
    setLevel(0);
    setPhase("requesting");
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      current.stream = stream;
      if (capture.current !== current) {
        stream.getTracks().forEach((track) => track.stop());
        return;
      }
      current.closeMeter = observeDictationVolume(stream, (next) => {
        if (capture.current === current) setLevel(next);
      });
      current.recording = config.onRealtimeVoice
        ? config.onRealtimeVoice(stream, current.controller.signal, (text) => {
          if (capture.current === current) {
            current.transcript = text;
            setHasTranscript(!!text.trim());
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
        setLevel(0);
        current.complete(true);
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

  return { phase, seconds, level, hasTranscript, error, start, stop, finish, cancel, isActive: () => capture.current !== null };
}
