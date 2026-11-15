"use client";
import { useEffect, useRef } from "react";
import type {
  MouseEvent as ReactMouseEvent,
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
  WheelEvent as ReactWheelEvent,
} from "react";

export interface MirrorVideoPoint {
  readonly x: number;
  readonly y: number;
}

export function MirrorVideo({
  stream,
  label,
  onFrame,
  ready,
  fit = "contain",
  zoom = 1,
  pan = { x: 0, y: 0 },
  interactive = false,
  onPointerDown,
  onPointerMove,
  onPointerUp,
  onPointerCancel,
  onDoubleClick,
  onWheel,
  onKeyDown,
}: {
  readonly stream: MediaStream;
  readonly label: string;
  readonly onFrame: () => void;
  readonly ready: boolean;
  readonly fit?: "contain" | "cover" | "actual";
  readonly zoom?: number;
  readonly pan?: { readonly x: number; readonly y: number };
  readonly interactive?: boolean;
  readonly onPointerDown?: (point: MirrorVideoPoint, event: ReactPointerEvent<HTMLVideoElement>) => void;
  readonly onPointerMove?: (point: MirrorVideoPoint, event: ReactPointerEvent<HTMLVideoElement>) => void;
  readonly onPointerUp?: (point: MirrorVideoPoint, event: ReactPointerEvent<HTMLVideoElement>) => void;
  readonly onPointerCancel?: (event: ReactPointerEvent<HTMLVideoElement>) => void;
  readonly onDoubleClick?: (point: MirrorVideoPoint, event: ReactMouseEvent<HTMLVideoElement>) => void;
  readonly onWheel?: (point: MirrorVideoPoint, event: ReactWheelEvent<HTMLVideoElement>) => void;
  readonly onKeyDown?: (event: ReactKeyboardEvent<HTMLVideoElement>) => void;
}) {
  const video = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    const element = video.current;
    if (!element) return;
    element.srcObject = stream;
    return () => {
      element.srcObject = null;
    };
  }, [stream]);
  return (
    <video
      ref={video}
      autoPlay
      muted
      playsInline
      draggable={false}
      aria-label={label}
      tabIndex={interactive ? 0 : undefined}
      onPointerDown={(event) => {
        const point = videoPoint(event.currentTarget, event.clientX, event.clientY, fit, zoom, pan);
        if (point) onPointerDown?.(point, event);
      }}
      onPointerMove={(event) => {
        const point = videoPoint(event.currentTarget, event.clientX, event.clientY, fit, zoom, pan);
        if (point) onPointerMove?.(point, event);
      }}
      onPointerUp={(event) => {
        const point = videoPoint(event.currentTarget, event.clientX, event.clientY, fit, zoom, pan);
        if (point) onPointerUp?.(point, event);
      }}
      onPointerCancel={onPointerCancel}
      onDoubleClick={(event) => {
        const point = videoPoint(event.currentTarget, event.clientX, event.clientY, fit, zoom, pan);
        if (point) onDoubleClick?.(point, event);
      }}
      onWheel={(event) => {
        const point = videoPoint(event.currentTarget, event.clientX, event.clientY, fit, zoom, pan);
        if (point) onWheel?.(point, event);
      }}
      onKeyDown={onKeyDown}
      onLoadedData={(event) => {
        if (
          event.currentTarget.readyState >= 2 &&
          event.currentTarget.videoWidth > 0
        )
          onFrame();
      }}
      style={{
        transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`,
        transformOrigin: "center center",
        cursor: interactive ? "crosshair" : undefined,
        touchAction: interactive ? "none" : undefined,
      }}
      className={
        fit === "actual"
          ? `max-h-none max-w-none ${ready ? "" : "invisible"}`
          : `h-full w-full ${fit === "cover" ? "object-cover" : "object-contain"} ${ready ? "" : "invisible"}`
      }
    />
  );
}

function videoPoint(
  video: HTMLVideoElement,
  clientX: number,
  clientY: number,
  fit: "contain" | "cover" | "actual",
  zoom: number,
  pan: { readonly x: number; readonly y: number },
): MirrorVideoPoint | null {
  if (video.videoWidth <= 0 || video.videoHeight <= 0) return null;
  const rect = video.getBoundingClientRect();
  if (fit === "actual") {
    return normalized(clientX, clientY, rect.left, rect.top, rect.width, rect.height);
  }
  const sourceAspect = video.videoWidth / video.videoHeight;
  const boxAspect = video.clientWidth / video.clientHeight;
  let width = video.clientWidth;
  let height = video.clientHeight;
  if (fit === "contain" ? sourceAspect > boxAspect : sourceAspect < boxAspect) {
    height = width / sourceAspect;
  } else {
    width = height * sourceAspect;
  }
  width *= zoom;
  height *= zoom;
  const centerX = rect.left + rect.width / 2 + pan.x;
  const centerY = rect.top + rect.height / 2 + pan.y;
  return normalized(clientX, clientY, centerX - width / 2, centerY - height / 2, width, height);
}

function normalized(
  clientX: number,
  clientY: number,
  left: number,
  top: number,
  width: number,
  height: number,
): MirrorVideoPoint | null {
  if (width <= 0 || height <= 0) return null;
  const x = (clientX - left) / width;
  const y = (clientY - top) / height;
  return x >= 0 && x <= 1 && y >= 0 && y <= 1 ? { x, y } : null;
}
