"use client";
import { useEffect, useRef, useState } from "react";
import { getApi } from "@multica/core/api";
import type { MirrorVideoStats } from "./video-stats";
import type {
  MirrorSourceBinding,
  MirrorVideoProfile,
  VscreenScope,
  VscreenVideoMetadata,
  VscreenVideoQuality,
} from "@multica/core/types";
import {
  MirrorVideoSession,
  type MirrorVideoState,
} from "./video-session";

export function useVideoSession(
  scope: VscreenScope,
  binding: MirrorSourceBinding | null,
  retry: number,
  videoProfile: MirrorVideoProfile = "auto",
) {
  const [stream, setStream] = useState<MediaStream | null>(null);
  const [statistics, setStatistics] = useState<MirrorVideoStats | null>(null);
  const [quality, setQuality] = useState<VscreenVideoQuality | null>(null);
  const [metadata, setMetadata] = useState<VscreenVideoMetadata | null>(null);
  const [status, setStatus] = useState<{
    state: MirrorVideoState;
    reason?: string;
  }>({ state: "closed" });
  const [reconnecting, setReconnecting] = useState(false);
  const [retryAttempt, setRetryAttempt] = useState(0);
  const [retryAt, setRetryAt] = useState<number | null>(null);
  const [autoRetryPaused, setAutoRetryPaused] = useState(false);
  const [autoRetry, setAutoRetry] = useState(0);
  const autoRetryAttempt = useRef(0);
  const autoRetryTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const sessionRef = useRef<MirrorVideoSession | null>(null);
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
    videoProfile,
  ]);
  const renderedIdentity = useRef(identity);
  useEffect(() => {
    let active = true;
    const session = binding
      ? new MirrorVideoSession({
          api: getApi().vscreen(scope),
          binding,
          videoProfile,
          callbacks: {
            statistics: (value) => {
              if (active) setStatistics(value);
            },
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
              if (active) {
                setStatus({ state, reason });
                if (state === "decoding" || state === "streaming") {
                  autoRetryAttempt.current = 0;
                  setRetryAttempt(0);
                  setRetryAt(null);
                  setReconnecting(false);
                }
              }
            },
          },
        })
      : null;
    sessionRef.current = session;
    renderedIdentity.current = identity;
    setStream(null);
    setMetadata(null);
    setQuality(null);
    setStatistics(null);
    setStatus({ state: binding ? "preparing" : "closed" });
    setReconnecting(false);
    setRetryAttempt(0);
    setRetryAt(null);
    setAutoRetryPaused(false);
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
      clearTimeout(autoRetryTimer.current);
      autoRetryTimer.current = undefined;
      sessionRef.current = null;
      closing.current = session?.close() ?? Promise.resolve();
    };
    // The serialized identity is the exact immutable peer scope and source binding.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [identity, retry, autoRetry]);

  useEffect(() => {
    // Only retry failures that can clear without changing permissions or the
    // selected source. Permanent capture/auth failures remain actionable.
    const retryable = new Set([
      "transport",
      "negotiation_timeout",
      "negotiation-failed",
      "negotiation_failed",
      "session-expired",
    ]);
    if (
      autoRetryPaused ||
      !binding ||
      status.state !== "failed" ||
      !retryable.has(status.reason ?? "")
    ) {
      return;
    }
    const attempt = autoRetryAttempt.current;
    autoRetryAttempt.current += 1;
    const delay = Math.min(1_000 * 2 ** Math.min(attempt, 4), 16_000);
    setRetryAttempt(attempt + 1);
    setRetryAt(Date.now() + delay);
    setReconnecting(true);
    autoRetryTimer.current = setTimeout(() => {
      autoRetryTimer.current = undefined;
      setRetryAt(null);
      setAutoRetry((value) => value + 1);
    }, delay);
    return () => {
      clearTimeout(autoRetryTimer.current);
      autoRetryTimer.current = undefined;
    };
  }, [autoRetryPaused, binding, status.reason, status.state]);
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
    statistics: current ? statistics : null,
    reconnecting,
    retryAttempt: current ? retryAttempt : 0,
    retryAt: current ? retryAt : null,
    cancelAutoRetry: () => {
      clearTimeout(autoRetryTimer.current);
      autoRetryTimer.current = undefined;
      setRetryAt(null);
      setReconnecting(false);
      setAutoRetryPaused(true);
    },
    retryNow: () => {
      setAutoRetryPaused(false);
      setRetryAt(null);
      setReconnecting(true);
      setAutoRetry((value) => value + 1);
    },
    onFrame,
    ...(current
      ? status
      : { state: binding ? ("preparing" as const) : ("closed" as const) }),
  };
}
