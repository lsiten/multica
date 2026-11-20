import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { LocalVoiceAdapter } from "@multica/core/platform";
import type { DictationDraft } from "../../editor/dictation-draft";
import { recordDictation, type DictationRecording } from "./chat-dictation-recorder";
import { observeDictationVolume } from "./dictation-volume";
import { dictationErrorKind, type DictationErrorKind } from "./chat-dictation-error";

export type DictationPhase = "idle" | "requesting" | "recording" | "paused" | "transcribing";
export const DICTATION_LIMIT_SECONDS = 60;
type Capture = {
  controller: AbortController;
  draft: DictationDraft;
  stream?: MediaStream;
  recording?: DictationRecording;
  timer?: ReturnType<typeof setTimeout>;
  startedAt: number;
  elapsedMs: number;
  paused: boolean;
  audio?: Blob;
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
  const [errorKind, setErrorKind] = useState<DictationErrorKind>("transcription");
  const [errorCause, setErrorCause] = useState<unknown>();
  const retryAudio = useRef<Blob | null>(null);
  const [canRetry, setCanRetry] = useState(false);

  const cancel = useCallback(() => {
    retryAudio.current = null;
    setCanRetry(false);
    setError(false);
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

  const fail = useCallback((current: Capture, cause?: unknown, kind = dictationErrorKind(cause)) => {
    if (capture.current !== current) return;
    capture.current = null;
    releaseCapture(current);
    if (current.transcript.trim()) current.draft.finish();
    else current.draft.cancel();
    currentOptions.current.onActiveChange(false);
    setPhase("idle");
    setLevel(0);
    setError(true);
    setErrorKind(kind);
    setErrorCause(cause);
    retryAudio.current = kind === "transcription" ? current.audio ?? null : null;
    setCanRetry(retryAudio.current !== null);
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
    } catch {
      fail(current);
    }
    return current.completion;
  }, [fail]);
  const stop = useCallback(() => { void finish(); }, [finish]);

  const togglePause = useCallback(() => {
    const current = capture.current;
    if (!current?.recording || current.stopping) return;
    current.paused = !current.paused;
    current.stream?.getTracks().forEach((track) => { track.enabled = !current.paused; });
    if (current.paused) {
      current.elapsedMs += Date.now() - current.startedAt;
      clearTimeout(current.timer);
      current.recording.pause?.();
      setLevel(0);
      setPhase("paused");
    } else {
      current.startedAt = Date.now();
      current.recording.resume?.();
      current.timer = setTimeout(stop, DICTATION_LIMIT_SECONDS * 1000 - current.elapsedMs);
      setPhase("recording");
    }
  }, [stop]);

  const start = useCallback(async (audio?: Blob) => {
    const config = currentOptions.current;
    if (!config.enabled || capture.current) return;
    setError(false);
    retryAudio.current = null;
    setCanRetry(false);
    if (!navigator.mediaDevices?.getUserMedia ||
      (!config.onRealtimeVoice && typeof MediaRecorder === "undefined")) {
      setError(true);
      setErrorKind("unsupported");
      return;
    }
    const draft = config.onBegin();
    if (!draft) return;
    let resolveCompletion!: (success: boolean) => void;
    const completion = new Promise<boolean>((resolve) => { resolveCompletion = resolve; });
    const current: Capture = {
      controller: new AbortController(), draft, startedAt: 0, elapsedMs: 0, paused: false, stopping: false, transcript: "",
      completion, complete: resolveCompletion,
    };
    capture.current = current;
    config.onActiveChange(true);
    setSeconds(0);
    setLevel(0);
    setPhase("requesting");
    try {
      const transcribe = (blob: Blob, signal: AbortSignal) => {
        current.audio = blob;
        return config.onVoice(blob, signal);
      };
      const complete = (text: string) => {
        if (capture.current !== current) return;
        if (!text.trim()) { fail(current, undefined, "empty"); return; }
        capture.current = null;
        releaseCapture(current);
        draft.update(text.trim());
        draft.finish();
        config.onActiveChange(false);
        setPhase("idle");
        setLevel(0);
        current.complete(true);
      };
      if (audio) {
        current.audio = audio;
        current.stopping = true;
        setPhase("transcribing");
        current.timer = setTimeout(() => fail(current), 120_000);
        void transcribe(audio, current.controller.signal).then(complete, (cause: unknown) => fail(current, cause));
        return;
      }
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      current.stream = stream;
      if (capture.current !== current) {
        stream.getTracks().forEach((track) => track.stop());
        return;
      }
      current.closeMeter = observeDictationVolume(stream, (next) => {
        if (capture.current === current && !current.paused) setLevel(next);
      });
      current.recording = config.onRealtimeVoice
        ? config.onRealtimeVoice(stream, current.controller.signal, (text) => {
          if (capture.current === current) {
            current.transcript = text;
            draft.update(text);
          }
        })
        : recordDictation(stream, current.controller.signal, transcribe);
      current.startedAt = Date.now();
      setPhase("recording");
      current.timer = setTimeout(stop, DICTATION_LIMIT_SECONDS * 1000);
      void current.recording.promise.then(complete, (cause: unknown) => fail(current, cause));
    } catch (cause) {
      fail(current, cause);
    }
  }, [fail, stop]);

  useLayoutEffect(() => {
    if (!options.enabled) cancel();
    return cancel;
  }, [options.enabled, cancel]);

  useEffect(() => {
    if (phase !== "recording") return;
    const timer = setInterval(() => {
      const current = capture.current;
      if (current) setSeconds(Math.min(DICTATION_LIMIT_SECONDS, Math.floor((current.elapsedMs + Date.now() - current.startedAt) / 1000)));
    }, 250);
    return () => clearInterval(timer);
  }, [phase]);

  useEffect(() => {
    if (phase === "idle") return;
    const onVisibility = () => {
      if (!document.hidden) return;
      const current = capture.current;
      if (current && !current.recording && !current.audio) cancel();
      else stop();
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [phase, cancel, stop]);

  const retry = () => { if (retryAudio.current) void start(retryAudio.current); };
  return { phase, seconds, level, error, errorKind, errorCause, canRetry, retry, togglePause, start, stop, finish, cancel, isActive: () => capture.current !== null };
}
