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
              if (active) setStatus({ state, reason });
            },
            control: (value) => {
              if (active) setControlState(value);
            },
            transcript: (value) => {
              if (active) setVoiceTranscript(value);
            },
            authorization: (value) => {
              if (active) setAuthorization(value);
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
      sessionRef.current = null;
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
  const currentSession = current ? sessionRef.current : null;
  return {
    stream: current ? stream : null,
    metadata: current ? metadata : null,
    quality: current ? quality : null,
    control: current ? controlState : { status: "inactive" as const },
    startControl: () => currentSession?.startControl(),
    stopControl: () => currentSession?.stopControl(),
    sendInput: (input: MirrorControlInput) => currentSession?.sendInput(input),
    sendVoice: (recording: Blob) => currentSession?.sendVoice(recording),
    voiceTranscript: current ? voiceTranscript : "",
    authorization: current ? authorization : null,
    respondAuthorization: (requestId: string, approved: boolean) => currentSession?.respondAuthorization(requestId, approved),
    onFrame,
    ...(current
      ? status
      : { state: binding ? ("preparing" as const) : ("closed" as const) }),
  };
}
