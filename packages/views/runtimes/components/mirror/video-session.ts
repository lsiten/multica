import { prepareVscreenReceiveOffer } from "@multica/core/runtimes/vscreen-receive-offer";
import {
  parseVscreenVideoMetadata,
  vscreenErrorReason,
  type VscreenApi,
} from "@multica/core/api";
import type {
  MirrorSourceBinding,
  VscreenViewerSession,
} from "@multica/core/types";
import { peerIceServers } from "./mirror-peer";
import { gatherVideoIce } from "./video-ice";

import {
  MirrorVideoError,
  type MirrorVideoCallbacks,
} from "./video-session-types";
export { MirrorVideoError } from "./video-session-types";
export type {
  MirrorVideoState,
  MirrorVideoCallbacks,
} from "./video-session-types";

/** Owns one read-only peer and its renewable grant, never an AI input target. */
export class MirrorVideoSession {
  private peer: RTCPeerConnection | null = null;
  private viewer: VscreenViewerSession | null = null;
  private disposed = false;
  private renewal: ReturnType<typeof setTimeout> | undefined;
  private expiry: ReturnType<typeof setTimeout> | undefined;
  private geometryRevision: number | undefined;
  private controlOpen = false;
  private metadataSeen = false;
  private stream: MediaStream | null = null;
  private published = false;
  private starting: Promise<void> | null = null;
  private readonly abort = new AbortController();

  constructor(
    private readonly options: {
      readonly api: VscreenApi;
      readonly binding: MirrorSourceBinding;
      readonly callbacks: MirrorVideoCallbacks;
    },
  ) {}

  private publishStream(): void {
    if (
      !this.disposed &&
      !this.published &&
      this.controlOpen &&
      this.metadataSeen &&
      this.stream
    ) {
      this.published = true;
      this.options.callbacks.stream(this.stream);
      this.options.callbacks.state("decoding");
      clearTimeout(this.renewal);
      this.renewal = setTimeout(() => void this.renew(), 10_000);
    }
  }

  private armGrant(expiresAt: string): void {
    clearTimeout(this.expiry);
    this.expiry = setTimeout(
      () => this.fail(new MirrorVideoError("viewer_revoked")),
      Math.max(0, Date.parse(expiresAt) - Date.now()),
    );
    clearTimeout(this.renewal);
    this.renewal = setTimeout(() => void this.renew(), 10_000);
  }

  private async renew(): Promise<void> {
    if (this.disposed || !this.viewer) return;
    if (!this.controlOpen || !this.metadataSeen) {
      this.renewal = setTimeout(() => void this.renew(), 1_000);
      return;
    }
    try {
      const grant = await this.options.api.renewMirrorSession(this.viewer);
      if (this.disposed) return;
      if (!grant) throw new MirrorVideoError("viewer_revoked");
      this.armGrant(grant.expiresAt);
    } catch (error) {
      this.fail(error);
    }
  }

  private controlMessage(value: unknown): void {
    if (this.disposed || typeof value !== "string") return;
    let raw: unknown;
    try {
      raw = JSON.parse(value);
    } catch (error) {
      if (error instanceof SyntaxError) return;
      throw error;
    }
    if (!raw || typeof raw !== "object") return;
    if ("type" in raw && raw.type === "mirror:error") {
      this.fail(
        new MirrorVideoError(
          "reason" in raw && typeof raw.reason === "string"
            ? raw.reason
            : "transport",
        ),
      );
      return;
    }
    if (
      !("geometry_revision" in raw) ||
      typeof raw.geometry_revision !== "number"
    )
      return;
    const metadata = parseVscreenVideoMetadata(raw, {
      binding: this.options.binding,
      geometryRevision: this.geometryRevision ?? raw.geometry_revision,
    });
    if (!metadata) {
      this.fail(new MirrorVideoError("source_gone"));
      return;
    }
    this.geometryRevision = metadata.geometryRevision;
    this.metadataSeen = true;
    this.options.callbacks.metadata(metadata);
    this.publishStream();
  }

  start(): Promise<void> {
    this.starting = this.run();
    return this.starting;
  }

  private async run(): Promise<void> {
    try {
      this.options.callbacks.state("preparing");
      const ice = await this.options.api.getIceConfig(this.abort.signal);
      if (this.disposed) return;
      if (!ice || typeof RTCPeerConnection === "undefined")
        throw new MirrorVideoError("webrtc_unavailable");
      const peer = new RTCPeerConnection({
        iceServers: peerIceServers(ice.iceServers),
      });
      this.peer = peer;
      peer.addTransceiver("video", { direction: "recvonly" });
      peer.ontrack = (event) => {
        if (this.disposed) {
          event.track.stop();
          return;
        }
        this.stream = event.streams[0] ?? new MediaStream([event.track]);
        event.track.onended = () =>
          this.fail(new MirrorVideoError("source_gone"));
        this.publishStream();
      };
      peer.onconnectionstatechange = () => {
        if (
          peer.connectionState === "failed" ||
          peer.connectionState === "disconnected" ||
          peer.connectionState === "closed"
        )
          this.fail(new MirrorVideoError("transport"));
      };
      const control = peer.createDataChannel("mirror-control", {
        ordered: true,
      });
      control.onopen = () => {
        this.controlOpen = true;
        this.publishStream();
      };
      control.onmessage = (event: MessageEvent<unknown>) =>
        this.controlMessage(event.data);
      control.onclose = () => {
        this.controlOpen = false;
        this.fail(new MirrorVideoError("viewer_revoked"));
      };
      control.onerror = () => this.fail(new MirrorVideoError("transport"));
      this.options.callbacks.state("negotiating");
      const createdOffer = await peer.createOffer();
      if (this.disposed) return;
      const receiveOffer = await prepareVscreenReceiveOffer(createdOffer, {
        signal: this.abort.signal,
      });
      if (this.disposed) return;
      await peer.setLocalDescription(receiveOffer);
      if (this.disposed) return;
      await gatherVideoIce(peer, this.abort.signal);
      if (this.disposed) return;
      const offer = peer.localDescription;
      if (!offer?.sdp || offer.type !== "offer")
        throw new MirrorVideoError("transport");
      const viewerId = crypto.randomUUID();
      const created = await this.options.api.createMirrorSession({
        viewerId,
        offer: { type: "offer", sdp: offer.sdp },
        binding: this.options.binding,
      });
      if (!created?.viewerGrant) throw new MirrorVideoError("viewer_revoked");
      this.viewer = {
        sessionId: created.id,
        viewerId,
        binding: this.options.binding,
      };
      if (this.disposed) {
        await this.closeRemote();
        return;
      }
      this.armGrant(created.viewerGrant.expiresAt);
      const deadline = Date.now() + 30_000;
      while (!this.disposed && Date.now() < deadline) {
        const session = await this.options.api.getMirrorSession(
          this.viewer,
          this.abort.signal,
        );
        if (this.disposed) return;
        if (!session || !["offered", "answered"].includes(session.state))
          throw new MirrorVideoError(session?.failureReason ?? "transport");
        if (session.videoQuality)
          this.options.callbacks.quality?.(session.videoQuality);
        if (session.answer) {
          await peer.setRemoteDescription(session.answer);
          return;
        }
        await new Promise<void>((resolve) => setTimeout(resolve, 300));
      }
      if (!this.disposed) throw new MirrorVideoError("negotiation_timeout");
    } catch (error) {
      this.fail(error);
    }
  }

  private fail(error: unknown): void {
    if (this.disposed) return;
    this.options.callbacks.state(
      "failed",
      error instanceof MirrorVideoError
        ? error.reason
        : (vscreenErrorReason(error) ?? "transport"),
    );
    void this.close();
  }

  private async closeRemote(): Promise<void> {
    const viewer = this.viewer;
    this.viewer = null;
    if (!viewer) return;
    try {
      await this.options.api.closeMirrorSession(viewer);
    } catch (error) {
      if (error instanceof Error)
        console.warn("Mirror viewer cleanup failed", { name: error.name });
      else throw error;
    }
  }

  async close(): Promise<void> {
    this.disposed = true;
    this.abort.abort();
    clearTimeout(this.renewal);
    clearTimeout(this.expiry);
    this.peer?.close();
    this.stream?.getTracks().forEach((track) => track.stop());
    this.options.callbacks.stream(null);
    this.options.callbacks.metadata(null);
    await this.starting;
    await this.closeRemote();
  }
}
