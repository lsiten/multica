import { prepareVscreenReceiveOffer } from "@multica/core/runtimes/vscreen-receive-offer";
import {
  parseVscreenVideoMetadata,
  vscreenErrorReason,
  type VscreenApi,
} from "@multica/core/api";
import type {
  MirrorSourceBinding,
  MirrorVideoProfile,
  VscreenViewerSession,
} from "@multica/core/types";
import { peerIceServers } from "./mirror-peer";
import { gatherVideoIce } from "./video-ice";
import { observeMirrorVideoStats } from "./video-stats";
import {
  MirrorVideoError,
  type MirrorVideoCallbacks,
} from "./video-session-types";

export { MirrorVideoError } from "./video-session-types";
export type {
  MirrorVideoState,
  MirrorVideoCallbacks,
} from "./video-session-types";

/** Owns one read-only peer for a renewable viewer grant. */
export class MirrorVideoSession {
  private peer: RTCPeerConnection | null = null;
  private viewer: VscreenViewerSession | null = null;
  private disposed = false;
  private renewal: ReturnType<typeof setTimeout> | undefined;
  private expiry: ReturnType<typeof setTimeout> | undefined;
  private geometryRevision: number | undefined;
  private metadataSeen = false;
  private stream: MediaStream | null = null;
  private published = false;
  private starting: Promise<void> | null = null;
  private readonly abort = new AbortController();
  private connectionGrace: ReturnType<typeof setTimeout> | undefined;

  constructor(
    private readonly options: {
      readonly api: VscreenApi;
      readonly binding: MirrorSourceBinding;
      readonly videoProfile?: MirrorVideoProfile;
      readonly callbacks: MirrorVideoCallbacks;
    },
  ) {}

  private publishStream(): void {
    if (
      !this.disposed &&
      !this.published &&
      this.metadataSeen &&
      this.stream
    ) {
      this.published = true;
      this.options.callbacks.stream(this.stream);
      this.options.callbacks.state("decoding");
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
    if (!this.metadataSeen) {
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

  private videoMetadataMessage(value: unknown): void {
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
        iceTransportPolicy: ice.iceTransportPolicy === "relay" ? "relay" : "all",
      });
      this.peer = peer;
      if (this.options.callbacks.statistics) {
        observeMirrorVideoStats(
          peer,
          this.abort.signal,
          this.options.callbacks.statistics,
        );
      }
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
          peer.connectionState === "connected" ||
          peer.connectionState === "connecting" ||
          peer.connectionState === "new"
        ) {
          clearTimeout(this.connectionGrace);
          this.connectionGrace = undefined;
          return;
        }
        if (peer.connectionState === "disconnected") {
          clearTimeout(this.connectionGrace);
          this.connectionGrace = setTimeout(() => {
            this.connectionGrace = undefined;
            if (peer.connectionState === "disconnected")
              this.fail(new MirrorVideoError("transport"));
          }, 10_000);
          return;
        }
        if (
          peer.connectionState === "failed" ||
          peer.connectionState === "closed"
        )
          this.fail(new MirrorVideoError("transport"));
      };
      const dataChannel = peer.createDataChannel("mirror-metadata", {
        ordered: true,
      });
      dataChannel.onmessage = (event: MessageEvent<unknown>) =>
        this.videoMetadataMessage(event.data);
      dataChannel.onclose = () => {
        if (this.disposed) return;
        const reason = peer.connectionState === "failed" || peer.connectionState === "disconnected"
          ? "transport"
          : "viewer_revoked";
        this.fail(new MirrorVideoError(reason));
      };
      dataChannel.onerror = () => this.fail(new MirrorVideoError("transport"));
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
        videoProfile: this.options.videoProfile,
      });
      if (!created?.viewerGrant)
        throw new MirrorVideoError("viewer_revoked");
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
    clearTimeout(this.connectionGrace);
    this.connectionGrace = undefined;
    this.peer?.close();
    this.stream?.getTracks().forEach((track) => track.stop());
    this.options.callbacks.stream(null);
    this.options.callbacks.metadata(null);
    await this.starting;
    await this.closeRemote();
  }
}
