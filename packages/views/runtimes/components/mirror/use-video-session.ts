"use client";
import { useEffect, useRef, useState } from "react";
import { getApi } from "@multica/core/api";
import type {
  MirrorSourceBinding,
  VscreenScope,
  VscreenVideoMetadata,
  VscreenVideoQuality,
} from "@multica/core/types";
import { MirrorVideoSession, type MirrorVideoState } from "./video-session";

export function useVideoSession(
  scope: VscreenScope,
  binding: MirrorSourceBinding | null,
  retry: number,
) {
  const [stream, setStream] = useState<MediaStream | null>(null);
  const [quality, setQuality] = useState<VscreenVideoQuality | null>(null);
  const [metadata, setMetadata] = useState<VscreenVideoMetadata | null>(null);
  const [status, setStatus] = useState<{
    state: MirrorVideoState;
    reason?: string;
  }>({ state: "closed" });
  const closing = useRef(Promise.resolve());
  const identity = JSON.stringify([
    scope.backendIdentity,
    scope.accountId,
    scope.workspaceId,
    scope.runtimeId,
    binding?.resource.uid,
    binding?.source.kind,
    binding?.source.sourceId,
    binding?.nativeEpoch,
    binding?.generation,
  ]);
  const renderedIdentity = useRef(identity);
  useEffect(() => {
    let active = true;
    const session = binding
      ? new MirrorVideoSession({
          api: getApi().vscreen(scope),
          binding,
          callbacks: {
            quality: (value) => {
              if (active) setQuality(value);
            },
            stream: (value) => {
              if (active) setStream(value);
            },
            metadata: (value) => {
              if (active) setMetadata(value);
            },
            state: (state, reason) => {
              if (active) setStatus({ state, reason });
            },
          },
        })
      : null;
    renderedIdentity.current = identity;
    setStream(null);
    setMetadata(null);
    setQuality(null);
    setStatus({ state: binding ? "preparing" : "closed" });
    const pageClosed = () => {
      void session?.close();
    };
    window.addEventListener("pagehide", pageClosed);
    void closing.current.then(() => {
      if (active) return session?.start();
      return undefined;
    });
    return () => {
      window.removeEventListener("pagehide", pageClosed);
      active = false;
      closing.current = session?.close() ?? Promise.resolve();
    };
    // The serialized identity is the exact immutable peer scope and source binding.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [identity, retry]);
  const onFrame = () => {
    if (renderedIdentity.current !== identity) return;
    setStatus((current) =>
      current.state === "decoding" ? { state: "streaming" } : current,
    );
  };
  const current = renderedIdentity.current === identity;
  return {
    stream: current ? stream : null,
    metadata: current ? metadata : null,
    quality: current ? quality : null,
    onFrame,
    ...(current
      ? status
      : { state: binding ? ("preparing" as const) : ("closed" as const) }),
  };
}
