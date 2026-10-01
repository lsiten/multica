"use client";
import { useEffect, useRef, useState } from "react";
import { getApi } from "@multica/core/api";
import type {
  MirrorSourceBinding,
  VscreenScope,
  VscreenVideoMetadata,
  VscreenVideoQuality,
} from "@multica/core/types";
import {
  MirrorVideoSession,
  type MirrorControlInput,
  type MirrorControlState,
  type MirrorVideoState,
  type MirrorAuthorizationRequest,
} from "./video-session";

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
  const [controlState, setControlState] = useState<MirrorControlState>({
    status: "inactive",
  });
  const [voiceTranscript, setVoiceTranscript] = useState("");
  const [authorization, setAuthorization] = useState<MirrorAuthorizationRequest | null>(null);
  const [authorizationPending, setAuthorizationPending] = useState(false);
  const [authorizationFailed, setAuthorizationFailed] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);
  const [autoRetry, setAutoRetry] = useState(0);
  const autoRetryAttempt = useRef(0);
  const autoRetryTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const decisionInFlight = useRef(false);
  const authorizationID = useRef<string | null>(null);
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
              if (active) {
                setStatus({ state, reason });
                if (state === "decoding" || state === "streaming") {
                  autoRetryAttempt.current = 0;
                  setReconnecting(false);
                }
              }
            },
            control: (value) => {
              if (active) setControlState(value);
            },
            transcript: (value) => {
              if (active) setVoiceTranscript(value);
            },
            authorization: (value) => {
              if (active) {
                authorizationID.current = value.request_id;
                setAuthorization(value);
                setAuthorizationFailed(false);
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
    setStatus({ state: binding ? "preparing" : "closed" });
    setControlState({ status: "inactive" });
    setVoiceTranscript("");
    setAuthorization(null);
    authorizationID.current = null;
    setAuthorizationPending(false);
    setAuthorizationFailed(false);
    setReconnecting(false);
    decisionInFlight.current = false;
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
    if (!binding || status.state !== "failed" || !retryable.has(status.reason ?? "")) {
      return;
    }
    const attempt = autoRetryAttempt.current;
    autoRetryAttempt.current += 1;
    const delay = Math.min(1_000 * 2 ** Math.min(attempt, 4), 16_000);
    setReconnecting(true);
    autoRetryTimer.current = setTimeout(() => {
      autoRetryTimer.current = undefined;
      setAutoRetry((value) => value + 1);
    }, delay);
    return () => {
      clearTimeout(autoRetryTimer.current);
      autoRetryTimer.current = undefined;
    };
  }, [binding, status.reason, status.state]);
  useEffect(() => {
    if (!authorization) return;
    const delay = Math.max(0, Date.parse(authorization.expires_at) - Date.now());
    const timer = window.setTimeout(() => setAuthorization((current) =>
      current?.request_id === authorization.request_id ? null : current,
    ), delay);
    return () => window.clearTimeout(timer);
  }, [authorization]);
  const onFrame = () => {
    if (renderedIdentity.current !== identity) return;
    setStatus((current) =>
      current.state === "decoding" ? { state: "streaming" } : current,
    );
  };
  const current = renderedIdentity.current === identity;
  const currentSession = current ? sessionRef.current : null;
  return {
    stream: current ? stream : null,
    metadata: current ? metadata : null,
    quality: current ? quality : null,
    control: current ? controlState : { status: "inactive" as const },
    startControl: () => currentSession?.startControl(),
    stopControl: () => currentSession?.stopControl(),
    sendInput: (input: MirrorControlInput) => currentSession?.sendInput(input),
    sendVoice: (recording: Blob) => {
      setVoiceTranscript("");
      return currentSession?.sendVoice(recording);
    },
    streamVoice: (stream: unknown, signal: AbortSignal, onPartial: (text: string) => void) =>
      currentSession?.streamVoice(stream, signal, onPartial) ?? {
        stop: () => undefined,
        promise: Promise.reject(new Error("Mirror session unavailable")),
      },
    voiceTranscript: current ? voiceTranscript : "",
    authorization: current ? authorization : null,
    authorizationPending,
    authorizationFailed,
    reconnecting,
    dismissAuthorization: () => setAuthorization(null),
    respondAuthorization: async (requestId: string, approved: boolean) => {
      if (!currentSession || decisionInFlight.current) return;
      decisionInFlight.current = true;
      setAuthorizationPending(true);
      setAuthorizationFailed(false);
      const processed = await currentSession.respondAuthorization(requestId, approved);
      if (sessionRef.current !== currentSession) return;
      decisionInFlight.current = false;
      setAuthorizationPending(false);
      if (authorizationID.current !== requestId) return;
      if (processed) {
        setAuthorization((value) => value?.request_id === requestId ? null : value);
      } else {
        setAuthorizationFailed(true);
      }
    },
    onFrame,
    ...(current
      ? status
      : { state: binding ? ("preparing" as const) : ("closed" as const) }),
  };
}
