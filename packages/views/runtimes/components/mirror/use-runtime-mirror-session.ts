"use client";

import { useEffect, useRef, useState } from "react";
import { ApiError, api } from "@multica/core/api";
import { MirrorFrameReassembler } from "./frame-reassembler";
import {
  peerConfiguration,
  waitForIceGatheringComplete,
} from "./mirror-peer";
import {
  mirrorSessionFailureReason,
  type RuntimeMirrorFailureReason,
} from "./mirror-session-failure";

export type RuntimeMirrorTransportState =
  | "idle"
  | "preparing"
  | "negotiating"
  | "streaming"
  | "failed"
  | "closed";

type MirrorControlReason =
  | "permission-denied"
  | "unsupported"
  | "no-display"
  | "capture-unavailable";

interface MirrorControlMessage {
  readonly type: "mirror:error";
  readonly reason: MirrorControlReason;
}

class MirrorConnectionError extends Error {
  readonly name = "MirrorConnectionError";

  constructor(readonly reason: RuntimeMirrorFailureReason) {
    super(`Mirror connection failed: ${reason}`);
  }
}

function viewerId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `mirror-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function toArrayBuffer(data: ArrayBuffer | Blob): Promise<ArrayBuffer> {
  return data instanceof Blob ? data.arrayBuffer() : Promise.resolve(data);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function parseControlMessage(data: string): MirrorControlMessage | null {
  try {
    const raw: unknown = JSON.parse(data);
    if (!isRecord(raw) || raw.type !== "mirror:error") return null;
    switch (raw.reason) {
      case "permission-denied":
      case "unsupported":
      case "no-display":
      case "capture-unavailable":
        return { type: "mirror:error", reason: raw.reason };
      default:
        return null;
    }
  } catch {
    return null;
  }
}

function failureReasonFromError(error: unknown): RuntimeMirrorFailureReason {
  if (error instanceof MirrorConnectionError) return error.reason;
  if (error instanceof ApiError) {
    if (error.status === 501) return "unsupported";
    if (error.status === 409) return "offline";
  }
  return "unknown";
}

function reportCleanupError(error: unknown): void {
  if (error instanceof Error) {
    console.warn("Mirror cleanup failed", error);
    return;
  }
  console.warn("Mirror cleanup failed", { error });
}

export function useRuntimeMirrorSession({
  runtimeId,
  enabled,
  retryNonce = 0,
}: {
  runtimeId: string;
  enabled: boolean;
  retryNonce?: number;
}) {
  const [state, setState] = useState<RuntimeMirrorTransportState>(enabled ? "preparing" : "idle");
  const [failureReason, setFailureReason] = useState<RuntimeMirrorFailureReason | null>(null);
  const [imageUrl, setImageUrl] = useState<string | null>(null);
  const [turnConfigured, setTurnConfigured] = useState<boolean | null>(null);
  const imageUrlRef = useRef<string | null>(null);

  useEffect(() => {
    if (!enabled || !runtimeId) {
      setState("idle");
      setFailureReason(null);
      return;
    }
    let disposed = false;
    let failed = false;
    let peer: RTCPeerConnection | null = null;
    let sessionId: string | null = null;
    const currentViewerId = viewerId();
    const reassembler = new MirrorFrameReassembler();

    const fail = (error: unknown) => {
      if (disposed || failed) return;
      failed = true;
      const reason = failureReasonFromError(error);
      setFailureReason(reason);
      setState("failed");
      peer?.close();
      closeSession();
    };
    const closeSession = () => {
      if (!sessionId) return;
      const sessionToClose = sessionId;
      sessionId = null;
      api
        .closeMirrorSession(runtimeId, sessionToClose, currentViewerId)
        .catch((error: unknown) => reportCleanupError(error));
    };

    const run = async () => {
      try {
        if (typeof RTCPeerConnection === "undefined") {
          throw new MirrorConnectionError("webrtc-unavailable");
        }
        setState("preparing");
        setFailureReason(null);
        failed = false;
        const iceConfig = await api.getMirrorICEConfig(runtimeId);
        setTurnConfigured(iceConfig.turn_configured);
        peer = new RTCPeerConnection(peerConfiguration(iceConfig));
        const channel = peer.createDataChannel("mirror", { ordered: true });
        channel.binaryType = "arraybuffer";

        channel.onmessage = async (event: MessageEvent<unknown>) => {
          if (typeof event.data === "string") {
            const control = parseControlMessage(event.data);
            if (control) {
              peer?.close();
              fail(new MirrorConnectionError(control.reason));
            }
            return;
          }
          if (!(event.data instanceof ArrayBuffer) && !(event.data instanceof Blob)) return;
          const packet = await toArrayBuffer(event.data);
          const next = reassembler.push(packet);
          if (!next || disposed) return;
          const url = URL.createObjectURL(
            new Blob([new Uint8Array(next.jpeg)], { type: "image/jpeg" }),
          );
          if (imageUrlRef.current) URL.revokeObjectURL(imageUrlRef.current);
          imageUrlRef.current = url;
          setImageUrl(url);
          setState("streaming");
        };
        channel.onerror = () => fail(new MirrorConnectionError("transport"));
        channel.onclose = () => {
          if (!disposed) fail(new MirrorConnectionError("transport"));
        };
        peer.oniceconnectionstatechange = () => {
          if (peer?.iceConnectionState === "failed") {
            fail(new MirrorConnectionError("transport"));
          }
        };
        peer.onconnectionstatechange = () => {
          if (peer?.connectionState === "failed") {
            fail(new MirrorConnectionError("transport"));
          }
        };

        setState("negotiating");
        const offer = await peer.createOffer();
        await peer.setLocalDescription(offer);
        try {
          await waitForIceGatheringComplete(peer);
        } catch {
          throw new MirrorConnectionError("transport");
        }
        if (disposed) return;
        const localOffer = peer.localDescription;
        if (!localOffer || localOffer.type !== "offer" || !localOffer.sdp) {
          throw new MirrorConnectionError("transport");
        }
        const created = await api.createMirrorSession(runtimeId, {
          viewer_id: currentViewerId,
          offer: { type: localOffer.type, sdp: localOffer.sdp },
        });
        if (!created.id) throw new MirrorConnectionError("transport");
        sessionId = created.id;

        const deadline = Date.now() + 30_000;
        while (!disposed && Date.now() < deadline) {
          const session = await api.getMirrorSession(runtimeId, created.id);
          const sessionFailure = mirrorSessionFailureReason(session);
          if (sessionFailure) {
            throw new MirrorConnectionError(sessionFailure);
          }
          if (session.state === "answered" && session.answer) {
            if (session.answer.type !== "answer") {
              throw new MirrorConnectionError("transport");
            }
            await peer.setRemoteDescription({
              type: "answer",
              sdp: session.answer.sdp,
            });
            return;
          }
          await new Promise((resolve) => setTimeout(resolve, 300));
        }
        if (!disposed) throw new MirrorConnectionError("negotiation-timeout");
      } catch (error) {
        fail(error);
      }
    };
    void run();

    return () => {
      disposed = true;
      peer?.close();
      closeSession();
      if (imageUrlRef.current) {
        URL.revokeObjectURL(imageUrlRef.current);
        imageUrlRef.current = null;
      }
      setImageUrl(null);
      setFailureReason(null);
      setTurnConfigured(null);
      setState("closed");
    };
  }, [enabled, retryNonce, runtimeId]);

  return { state, failureReason, imageUrl, turnConfigured };
}
