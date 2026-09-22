"use client";

import { AudioLines, LoaderCircle, Mic } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

type Capture = {
  controller: AbortController;
  recorder?: MediaRecorder;
  stream?: MediaStream;
  timer?: ReturnType<typeof setTimeout>;
  chunks: Blob[];
  realtime?: { stop: () => void; promise: Promise<string> };
  transcriptAtStart: string | undefined;
  cancelled: boolean;
  released: boolean;
};

const RECORDING_LIMIT_MS = 60_000;
const VOICE_OVERLAY_GAP = 16;
const VOICE_OVERLAY_VIEWPORT_PADDING = 16;

type PointerPosition = {
  readonly x: number;
  readonly y: number;
};

type OverlayPlacement = "above" | "below";

function supportedMimeType(): string | undefined {
  if (typeof MediaRecorder === "undefined" || typeof MediaRecorder.isTypeSupported !== "function") return undefined;
  return ["audio/webm;codecs=opus", "audio/webm", "audio/mp4", "audio/ogg;codecs=opus"]
    .find((type) => MediaRecorder.isTypeSupported(type));
}

function formatDuration(durationMs: number): string {
  const seconds = Math.floor(durationMs / 1000);
  return `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}

function clampPointerPosition(clientX: number, clientY: number): PointerPosition {
  const maxX = Math.max(
    VOICE_OVERLAY_VIEWPORT_PADDING,
    window.innerWidth - VOICE_OVERLAY_VIEWPORT_PADDING,
  );
  const maxY = Math.max(
    VOICE_OVERLAY_VIEWPORT_PADDING,
    window.innerHeight - VOICE_OVERLAY_VIEWPORT_PADDING,
  );
  return {
    x: Math.min(maxX, Math.max(VOICE_OVERLAY_VIEWPORT_PADDING, clientX)),
    y: Math.min(maxY, Math.max(VOICE_OVERLAY_VIEWPORT_PADDING, clientY)),
  };
}

function dispose(capture: Capture) {
  capture.cancelled = true;
  capture.controller.abort();
  clearTimeout(capture.timer);
  capture.realtime?.stop();
  capture.realtime = undefined;
  if (capture.recorder?.state === "recording") capture.recorder.stop();
  capture.stream?.getTracks().forEach((track) => track.stop());
}

export function HoldToTalkInput({ enabled, transcript, onVoice, onTranscript, errorMessage, onRealtimeVoice }: {
  readonly enabled: boolean;
  readonly transcript?: string;
  readonly onVoice: (recording: Blob, signal: AbortSignal) => Promise<string | void> | void;
  readonly onTranscript: (text: string) => void;
  readonly errorMessage?: string;
  readonly onRealtimeVoice?: (stream: unknown, signal: AbortSignal, onPartial: (text: string) => void) => { stop: () => void; promise: Promise<string> };
}) {
  const { t } = useT("runtimes");
  const [phase, setPhase] = useState<"idle" | "requesting" | "recording" | "transcribing">("idle");
  const [cancelGesture, setCancelGesture] = useState(false);
  const [error, setError] = useState(false);
  const [recordingMs, setRecordingMs] = useState(0);
  const [pointerPosition, setPointerPosition] = useState<PointerPosition | null>(null);
  const [overlayAnchor, setOverlayAnchor] = useState<PointerPosition | null>(null);
  const [overlayPlacement, setOverlayPlacement] = useState<OverlayPlacement>("above");
  const [partialTranscript, setPartialTranscript] = useState<string>();
  const capture = useRef<Capture | null>(null);
  const cancelOnRelease = useRef(false);
  const pointer = useRef<number | null>(null);
  const overlayRef = useRef<HTMLDivElement | null>(null);
  const recordingStartedAt = useRef<number | null>(null);
  const previousTranscript = useRef(transcript);

  useEffect(() => {
    if (phase !== "recording" || recordingStartedAt.current === null) return;
    const timer = window.setInterval(() => {
      setRecordingMs(Date.now() - (recordingStartedAt.current ?? Date.now()));
    }, 100);
    return () => window.clearInterval(timer);
  }, [phase]);

  useEffect(() => {
    if (transcript === previousTranscript.current) return;
    const current = capture.current;
    if (!transcript) {
      previousTranscript.current = transcript;
      return;
    }
    if (!current?.released || current.cancelled) return;
    previousTranscript.current = transcript;
    dispose(current);
    capture.current = null;
    recordingStartedAt.current = null;
    setRecordingMs(0);
    setPhase("idle");
    onTranscript(transcript);
  }, [phase, transcript, onTranscript]);

  useEffect(() => {
    const cancel = () => {
      if (capture.current) dispose(capture.current);
      capture.current = null;
      pointer.current = null;
      setPointerPosition(null);
      setOverlayAnchor(null);
      setPartialTranscript(undefined);
      recordingStartedAt.current = null;
      setRecordingMs(0);
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
    recordingStartedAt.current = null;
    setRecordingMs(0);
    setPhase("idle");
    setError(true);
  };

  const release = (cancel = false) => {
    const current = capture.current;
    if (!current || current.released) return;
    current.released = true;
    clearTimeout(current.timer);
    if (cancel || (!current.recorder && !current.realtime)) {
      const realtime = current.realtime;
      current.realtime = undefined;
      dispose(current);
      realtime?.stop();
      capture.current = null;
      setPointerPosition(null);
      setOverlayAnchor(null);
      setPartialTranscript(undefined);
      recordingStartedAt.current = null;
      setRecordingMs(0);
      setPhase("idle");
      return;
    }
    if (current.realtime) {
      const realtime = current.realtime;
      current.realtime = undefined;
      realtime.stop();
      setPhase("transcribing");
      current.timer = setTimeout(() => fail(current), 120_000);
      void realtime.promise.then((text) => {
        if (capture.current !== current || current.cancelled) return;
        if (!text.trim()) { fail(current); return; }
        dispose(current); capture.current = null; recordingStartedAt.current = null;
        setRecordingMs(0); setPhase("idle"); onTranscript(text.trim());
      }).catch(() => fail(current));
      setPointerPosition(null); setOverlayAnchor(null);
      return;
    }
    const recorder = current.recorder;
    if (!recorder) return;
    recorder.stop();
    current.stream?.getTracks().forEach((track) => track.stop());
    setPointerPosition(null);
    setOverlayAnchor(null);
  };

  const start = async () => {
    if (!enabled || capture.current) return;
    setError(false);
    setPartialTranscript(undefined);
    setCancelGesture(false);
    cancelOnRelease.current = false;
    if (!navigator.mediaDevices?.getUserMedia || (!onRealtimeVoice && typeof MediaRecorder === "undefined")) {
      setError(true);
      return;
    }
    const current: Capture = {
      cancelled: false,
      released: false,
      controller: new AbortController(),
      chunks: [],
      transcriptAtStart: transcript,
    };
    capture.current = current;
    setPhase("requesting");
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      current.stream = stream;
      if (capture.current !== current || current.cancelled || current.released) {
        dispose(current);
        return;
      }
      if (onRealtimeVoice) {
        current.realtime = onRealtimeVoice(stream, current.controller.signal, (text) => {
          if (capture.current === current && !current.cancelled) setPartialTranscript(text.trim());
        });
        void current.realtime.promise.catch(() => fail(current));
        recordingStartedAt.current = Date.now();
        setRecordingMs(0);
        setPhase("recording");
        current.timer = setTimeout(() => release(cancelOnRelease.current), RECORDING_LIMIT_MS);
        return;
      }
      const mimeType = supportedMimeType();
      const recorder = new MediaRecorder(stream, {
        audioBitsPerSecond: 48_000,
        ...(mimeType ? { mimeType } : {}),
      });
      current.recorder = recorder;
      recorder.ondataavailable = (event) => {
        if (event.data.size) current.chunks.push(event.data);
      };
      recorder.onerror = () => fail(current);
      recorder.onstop = () => {
        stream.getTracks().forEach((track) => track.stop());
        if (capture.current !== current || current.cancelled) return;
        clearTimeout(current.timer);
        current.released = true;
        const blob = new Blob(current.chunks, { type: recorder.mimeType || "audio/webm" });
        if (!blob.size || blob.size > 512 * 1024) {
          fail(current);
          return;
        }
        setPhase("transcribing");
        current.timer = setTimeout(() => fail(current), 30_000);
        void Promise.resolve().then(async () => {
          if (!current.cancelled) return onVoice(blob, current.controller.signal);
        }).then((text) => {
          if (typeof text !== "string" || capture.current !== current || current.cancelled) return;
          if (!text.trim()) { fail(current); return; }
          dispose(current);
          capture.current = null;
          recordingStartedAt.current = null;
          setRecordingMs(0);
          setPhase("idle");
          onTranscript(text);
        }).catch(() => fail(current));
      };
      recorder.start();
      recordingStartedAt.current = Date.now();
      setRecordingMs(0);
      setPhase("recording");
      current.timer = setTimeout(() => release(cancelOnRelease.current), RECORDING_LIMIT_MS);
    } catch {
      fail(current);
    }
  };

  const holding = phase === "recording" || phase === "requesting";
  const liveTranscript = partialTranscript || (
    capture.current && transcript !== capture.current.transcriptAtStart
      ? transcript?.trim()
      : undefined
  );
  useLayoutEffect(() => {
    if (!pointerPosition || !overlayRef.current) return;
    const rect = overlayRef.current.getBoundingClientRect();
    const halfWidth = rect.width / 2;
    const minX = VOICE_OVERLAY_VIEWPORT_PADDING + halfWidth;
    const maxX = Math.max(
      minX,
      window.innerWidth - VOICE_OVERLAY_VIEWPORT_PADDING - halfWidth,
    );
    const x = Math.min(maxX, Math.max(minX, pointerPosition.x));
    const viewportHeight = Math.max(
      VOICE_OVERLAY_VIEWPORT_PADDING * 2,
      window.innerHeight,
    );
    const aboveMinY = VOICE_OVERLAY_VIEWPORT_PADDING + VOICE_OVERLAY_GAP + rect.height;
    const aboveMaxY = viewportHeight - VOICE_OVERLAY_VIEWPORT_PADDING + VOICE_OVERLAY_GAP;
    const belowMaxY =
      viewportHeight - VOICE_OVERLAY_VIEWPORT_PADDING - VOICE_OVERLAY_GAP - rect.height;
    const aboveFits = aboveMinY <= aboveMaxY && pointerPosition.y >= aboveMinY;
    const belowFits = belowMaxY >= VOICE_OVERLAY_VIEWPORT_PADDING && pointerPosition.y <= belowMaxY;
    const placement: OverlayPlacement = aboveFits || !belowFits ? "above" : "below";
    const y = placement === "above"
      ? Math.min(aboveMaxY, Math.max(aboveMinY, pointerPosition.y))
      : Math.min(belowMaxY, Math.max(VOICE_OVERLAY_VIEWPORT_PADDING, pointerPosition.y));
    setOverlayPlacement(placement);
    setOverlayAnchor((current) =>
      current?.x === x && current.y === y
        ? current
        : { x, y },
    );
  }, [cancelGesture, liveTranscript, phase, pointerPosition]);
  const anchor = overlayAnchor ?? pointerPosition;
  const overlayStyle = anchor
    ? {
        left: String(anchor.x) + "px",
        top: String(anchor.y) + "px",
        transform:
          overlayPlacement === "above"
            ? "translate(-50%, calc(-100% - 16px))"
            : "translate(-50%, 16px)",
      }
    : {
        left: "50%",
        top: "72%",
        transform: "translate(-50%, -100%)",
      };
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
          setPointerPosition(clampPointerPosition(event.clientX, event.clientY));
          event.currentTarget.setPointerCapture(event.pointerId);
          void start();
        }}
        onPointerMove={(event) => {
          if (pointer.current !== event.pointerId || !holding) return;
          setPointerPosition(clampPointerPosition(event.clientX, event.clientY));
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
          <div
            ref={overlayRef}
            data-voice-overlay
            className="absolute flex max-w-[calc(100vw-2rem)] flex-col items-center gap-3"
            style={overlayStyle}
          >
            {liveTranscript && (
              <div
                aria-live="polite"
                className="max-w-full rounded-lg border bg-background/95 px-3 py-2 text-body text-foreground shadow-md"
              >
                <span className="break-words [overflow-wrap:anywhere]">{liveTranscript}</span>
              </div>
            )}
            <div className={cn("relative flex flex-col items-center gap-4 rounded-3xl px-12 py-8 shadow-lg", cancelGesture ? "bg-destructive text-destructive-foreground" : "bg-primary text-primary-foreground")}>
              <AudioLines aria-hidden="true" className="size-12 motion-safe:animate-pulse" />
              <span role="status" className="text-body font-medium">
                {cancelGesture ? t(($) => $.vscreen.voice_cancel_release) : phase === "requesting" ? t(($) => $.vscreen.voice_requesting) : t(($) => $.vscreen.voice_release)}
              </span>
              {phase === "recording" && <span className="font-mono text-caption tabular-nums opacity-80">{formatDuration(recordingMs)}</span>}
            </div>
            <span className="text-body text-foreground">{t(($) => $.vscreen.voice_cancel_hint)}</span>
          </div>
        </div>
      )}
      {error && <p role="alert" className="mt-1 text-caption text-destructive">{errorMessage || t(($) => $.vscreen.voice_failed)}</p>}
    </div>
  );
}
