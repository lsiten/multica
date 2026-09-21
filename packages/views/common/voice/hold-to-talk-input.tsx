"use client";

import { AudioLines, LoaderCircle, Mic } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

type Capture = {
  controller: AbortController;
  recorder?: MediaRecorder;
  stream?: MediaStream;
  timer?: ReturnType<typeof setTimeout>;
  cancelled: boolean;
  released: boolean;
};

function dispose(capture: Capture) {
  capture.cancelled = true;
  capture.controller.abort();
  clearTimeout(capture.timer);
  if (capture.recorder?.state === "recording") capture.recorder.stop();
  capture.stream?.getTracks().forEach((track) => track.stop());
}

export function HoldToTalkInput({ enabled, transcript, onVoice, onTranscript, errorMessage }: {
  readonly enabled: boolean;
  readonly transcript?: string;
  readonly onVoice: (recording: Blob, signal: AbortSignal) => Promise<string | void> | void;
  readonly onTranscript: (text: string) => void;
  readonly errorMessage?: string;
}) {
  const { t } = useT("runtimes");
  const [phase, setPhase] = useState<"idle" | "requesting" | "recording" | "transcribing">("idle");
  const [cancelGesture, setCancelGesture] = useState(false);
  const [error, setError] = useState(false);
  const capture = useRef<Capture | null>(null);
  const cancelOnRelease = useRef(false);
  const pointer = useRef<number | null>(null);
  const previousTranscript = useRef(transcript);

  useEffect(() => {
    if (transcript === previousTranscript.current) return;
    previousTranscript.current = transcript;
    const current = capture.current;
    if (!transcript || !current?.released || current.cancelled) return;
    dispose(current);
    capture.current = null;
    setPhase("idle");
    onTranscript(transcript);
  }, [transcript, onTranscript]);

  useEffect(() => {
    const cancel = () => {
      if (capture.current) dispose(capture.current);
      capture.current = null;
      pointer.current = null;
      setPhase("idle");
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") cancel();
    };
    const onVisibility = () => {
      if (document.hidden) cancel();
    };
    if (!enabled) cancel();
    window.addEventListener("blur", cancel);
    window.addEventListener("keydown", onKey);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.removeEventListener("blur", cancel);
      window.removeEventListener("keydown", onKey);
      document.removeEventListener("visibilitychange", onVisibility);
      if (capture.current) dispose(capture.current);
      capture.current = null;
    };
  }, [enabled]);

  const fail = (current: Capture) => {
    if (capture.current !== current || current.cancelled) return;
    dispose(current);
    capture.current = null;
    setPhase("idle");
    setError(true);
  };

  const release = (cancel = false) => {
    const current = capture.current;
    if (!current || current.released) return;
    current.released = true;
    clearTimeout(current.timer);
    if (cancel || !current.recorder) {
      dispose(current);
      capture.current = null;
      setPhase("idle");
      return;
    }
    current.recorder.stop();
    current.stream?.getTracks().forEach((track) => track.stop());
  };

  const start = async () => {
    if (!enabled || capture.current) return;
    setError(false);
    setCancelGesture(false);
    cancelOnRelease.current = false;
    if (typeof MediaRecorder === "undefined" || !navigator.mediaDevices?.getUserMedia) {
      setError(true);
      return;
    }
    const current: Capture = { cancelled: false, released: false, controller: new AbortController() };
    capture.current = current;
    setPhase("requesting");
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      current.stream = stream;
      if (capture.current !== current || current.cancelled || current.released) {
        dispose(current);
        return;
      }
      const recorder = new MediaRecorder(stream, { audioBitsPerSecond: 48_000 });
      current.recorder = recorder;
      const chunks: Blob[] = [];
      recorder.ondataavailable = (event) => {
        if (event.data.size) chunks.push(event.data);
      };
      recorder.onerror = () => fail(current);
      recorder.onstop = () => {
        stream.getTracks().forEach((track) => track.stop());
        if (capture.current !== current || current.cancelled) return;
        clearTimeout(current.timer);
        current.released = true;
        const blob = new Blob(chunks, { type: recorder.mimeType || "audio/webm" });
        if (!blob.size || blob.size > 512 * 1024) {
          fail(current);
          return;
        }
        setPhase("transcribing");
        current.timer = setTimeout(() => fail(current), 30_000);
        void Promise.resolve().then(() => {
          if (!current.cancelled) return onVoice(blob, current.controller.signal);
        }).then((text) => {
          if (typeof text !== "string" || capture.current !== current || current.cancelled) return;
          if (!text.trim()) { fail(current); return; }
          dispose(current);
          capture.current = null;
          setPhase("idle");
          onTranscript(text);
        }).catch(() => fail(current));
      };
      recorder.start();
      setPhase("recording");
      current.timer = setTimeout(() => release(cancelOnRelease.current), 60_000);
    } catch {
      fail(current);
    }
  };

  const holding = phase === "recording" || phase === "requesting";
  return (
    <div className="min-w-0 flex-1">
      <Button
        size="lg"
        variant="secondary"
        className="w-full touch-none select-none"
        disabled={!enabled || phase === "transcribing"}
        aria-busy={phase === "requesting" || phase === "transcribing"}
        onContextMenu={(event) => event.preventDefault()}
        onPointerDown={(event) => {
          if (event.button !== 0 || pointer.current !== null) return;
          pointer.current = event.pointerId;
          event.currentTarget.setPointerCapture(event.pointerId);
          void start();
        }}
        onPointerMove={(event) => {
          if (pointer.current !== event.pointerId || !holding) return;
          const cancel = event.clientY < event.currentTarget.getBoundingClientRect().top - 40;
          cancelOnRelease.current = cancel;
          setCancelGesture(cancel);
        }}
        onPointerUp={(event) => {
          if (pointer.current !== event.pointerId) return;
          pointer.current = null;
          release(cancelOnRelease.current);
        }}
        onPointerCancel={() => { pointer.current = null; release(true); }}
        onLostPointerCapture={() => {
          if (pointer.current !== null) { pointer.current = null; release(true); }
        }}
        onKeyDown={(event) => {
          if (event.key !== " " && event.key !== "Enter") return;
          event.preventDefault();
          if (!event.repeat) void start();
        }}
        onKeyUp={(event) => {
          if (event.key !== " " && event.key !== "Enter") return;
          event.preventDefault();
          release();
        }}
        onBlur={() => release(true)}
      >
        {phase === "transcribing" ? <LoaderCircle className="size-4 motion-safe:animate-spin" /> : <Mic className="size-4" />}
        {phase === "transcribing" ? t(($) => $.vscreen.voice_transcribing) : t(($) => $.vscreen.voice_hold)}
      </Button>
      {holding && (
        <div className="pointer-events-none fixed inset-0 z-50 flex flex-col items-center justify-end gap-6 bg-background/70 px-6 pb-[20vh] backdrop-blur-sm">
          <div className={cn("relative flex flex-col items-center gap-4 rounded-3xl px-12 py-8 shadow-lg", cancelGesture ? "bg-destructive text-destructive-foreground" : "bg-primary text-primary-foreground")}>
            <AudioLines aria-hidden="true" className="size-12 motion-safe:animate-pulse" />
            <span role="status" className="text-body font-medium">
              {cancelGesture ? t(($) => $.vscreen.voice_cancel_release) : phase === "requesting" ? t(($) => $.vscreen.voice_requesting) : t(($) => $.vscreen.voice_release)}
            </span>
          </div>
          <span className="text-body text-foreground">{t(($) => $.vscreen.voice_cancel_hint)}</span>
        </div>
      )}
      {error && <p role="alert" className="mt-1 text-caption text-destructive">{errorMessage || t(($) => $.vscreen.voice_failed)}</p>}
    </div>
  );
}
