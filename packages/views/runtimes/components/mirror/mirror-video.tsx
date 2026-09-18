"use client";
import { useEffect, useRef } from "react";
export function MirrorVideo({
  stream,
  label,
  onFrame,
  ready,
}: {
  readonly stream: MediaStream;
  readonly label: string;
  readonly onFrame: () => void;
  readonly ready: boolean;
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
      aria-label={label}
      onLoadedData={(event) => {
        if (
          event.currentTarget.readyState >= 2 &&
          event.currentTarget.videoWidth > 0
        )
          onFrame();
      }}
      className={`h-full w-full object-contain ${ready ? "" : "invisible"}`}
    />
  );
}
