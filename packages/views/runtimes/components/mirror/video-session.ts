import { prepareVscreenReceiveOffer } from "@multica/core/runtimes/vscreen-receive-offer";
import {
  parseVscreenVideoMetadata,
  parseMirrorAuthorizationRequest,
  parseMirrorVoiceMessage,
  vscreenErrorReason,
  type VscreenApi,
} from "@multica/core/api";
import type {
  MirrorControlGrant,
  MirrorInputAck,
  MirrorSourceBinding,
  VscreenViewerSession,
} from "@multica/core/types";
import { peerIceServers } from "./mirror-peer";
import { gatherVideoIce } from "./video-ice";

import {
  MirrorVideoError,
  type MirrorControlInput,
  type MirrorControlState,
  type MirrorVideoCallbacks,
} from "./video-session-types";
export { MirrorVideoError } from "./video-session-types";
export type {
  MirrorControlInput,
  MirrorControlPointer,
  MirrorControlState,
  MirrorControlStatus,
  MirrorVideoState,
  MirrorVideoCallbacks,
  MirrorAuthorizationRequest,
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
  private controlChannel: RTCDataChannel | null = null;
  private inputChannel: RTCDataChannel | null = null;
  private voiceChannel: RTCDataChannel | null = null;
  private inputOpen = false;
  private inputReady: ((open: boolean) => void) | null = null;
  private controlGrant: MirrorControlGrant | null = null;
  private controlRenewal: ReturnType<typeof setTimeout> | undefined;
  private inputSeq = 0;
  private voiceSeq = 0;
  private readonly pendingAuthorizations = new Set<string>();
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

  private wireInputChannel(peer: RTCPeerConnection): RTCDataChannel {
    const channel = peer.createDataChannel("mirror-input", { ordered: true });
    channel.binaryType = "arraybuffer";
    channel.onopen = () => {
      this.inputOpen = true;
      this.inputReady?.(true);
      this.inputReady = null;
    };
    channel.onclose = () => {
      this.inputOpen = false;
      this.inputReady?.(false);
      this.inputReady = null;
      if (this.controlGrant) void this.endControl("viewer_revoked");
    };
    channel.onerror = () => {
      this.inputReady?.(false);
      this.inputReady = null;
    };
    channel.onmessage = (event: MessageEvent<unknown>) =>
      this.inputAckMessage(event.data);
    return channel;
  }

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

  private publishControlState(
    status: MirrorControlState["status"],
    reason?: string,
    lastAck?: MirrorInputAck,
  ): void {
    this.options.callbacks.control?.({
      status,
      reason,
      lastAck,
      ...(this.controlGrant ? { grant: this.controlGrant } : {}),
    });
  }

  private async ensureInputChannel(): Promise<void> {
    if (this.disposed) throw new MirrorVideoError("transport");
    if (this.inputOpen) return;
    if (!this.inputChannel) throw new MirrorVideoError("webrtc_unavailable");
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(
        () => reject(new MirrorVideoError("transport")),
        3_000,
      );
      this.inputReady = (open) => {
        clearTimeout(timer);
        if (open) resolve();
        else reject(new MirrorVideoError("viewer_revoked"));
      };
    });
  }

  private inputAckMessage(value: unknown): void {
    if (this.disposed || typeof value !== "string") return;
    let raw: unknown;
    try {
      raw = JSON.parse(value);
    } catch {
      return;
    }
    if (!raw || typeof raw !== "object" || !("type" in raw)) return;
    const type = raw.type;
    if (type !== "mirror-input:ack" && type !== "mirror-input:nack") return;
    const ack = raw as MirrorInputAck;
    if (type === "mirror-input:nack") {
      if (ack.reason === "busy") {
        this.publishControlState("active", undefined, ack);
        return;
      }
      if (ack.reason === "stale" || ack.reason === "denied") {
        this.endControl(ack.reason);
        return;
      }
    }
    this.publishControlState("active", undefined, ack);
  }

  private voiceMessage(value: unknown): void {
    if (this.disposed || typeof value !== "string") return;
    const raw = parseMirrorVoiceMessage(value);
    if (raw?.type === "mirror-voice:transcript") this.options.callbacks.transcript?.(raw.text);
  }

  private authorizationMessage(value: unknown): void {
    if (this.disposed || typeof value !== "string") return;
    const request = parseMirrorAuthorizationRequest(value);
    if (request) {
      this.pendingAuthorizations.add(request.request_id);
      this.options.callbacks.authorization?.(request);
    }
  }

  respondAuthorization(requestId: string, approved: boolean): void {
    const channel = this.controlChannel;
    if (this.pendingAuthorizations.has(requestId) && channel?.readyState === "open") {
      this.pendingAuthorizations.delete(requestId);
      channel.send(JSON.stringify({ type: "mirror-authorization:response", request_id: requestId, approved }));
    }
  }

  async sendVoice(recording: Blob): Promise<void> {
    if (this.disposed || !this.controlGrant) return;
    const channel = this.voiceChannel;
    if (channel.readyState !== "open") return;
    const bytes = new Uint8Array(await recording.arrayBuffer());
    if (bytes.byteLength === 0 || bytes.byteLength > 512 * 1024) return;
    let binary = "";
    for (const byte of bytes) binary += String.fromCharCode(byte);
    channel.send(JSON.stringify({
      type: "mirror-voice:audio",
      grant_id: this.controlGrant?.grantId ?? "",
      seq: ++this.voiceSeq,
      mime_type: recording.type || "audio/webm",
      audio_base64: btoa(binary),
    }));
  }

  private armControlRenewal(): void {
    clearTimeout(this.controlRenewal);
    this.controlRenewal = setTimeout(() => void this.renewControl(), 10_000);
  }

  private async renewControl(): Promise<void> {
    const viewer = this.viewer;
    const grant = this.controlGrant;
    if (!viewer || !grant || this.disposed) return;
    try {
      const renewed = await this.options.api.renewMirrorControlGrant(
        viewer,
        grant.source,
        grant.sourceGeneration,
        this.abort.signal,
      );
      if (this.disposed) return;
      this.controlGrant = renewed;
      this.armControlRenewal();
      this.publishControlState("active");
    } catch {
      this.endControl("stale");
    }
  }

  async startControl(): Promise<void> {
    const viewer = this.viewer;
    const binding = this.metadataSeen ? this.options.binding : null;
    if (!viewer || !binding || !this.controlOpen || !this.stream)
      throw new MirrorVideoError("interaction_unavailable");
    if (this.controlGrant) return;
    this.publishControlState("requesting");
    try {
      await this.ensureInputChannel();
      const grant = await this.options.api.createMirrorControlGrant(
        viewer,
        binding.source,
        binding.generation,
        this.abort.signal,
      );
      if (this.disposed) {
        await this.options.api.revokeMirrorControlGrant(viewer.viewerId).catch(() => undefined);
        return;
      }
      this.controlGrant = grant;
      this.armControlRenewal();
      this.publishControlState("active");
    } catch (error) {
      this.publishControlState(
        "failed",
        error instanceof MirrorVideoError
          ? error.reason
          : (vscreenErrorReason(error) ?? "denied"),
      );
      throw error;
    }
  }

  private async endControl(reason?: string): Promise<void> {
    clearTimeout(this.controlRenewal);
    const viewer = this.viewer;
    const hadGrant = this.controlGrant;
    this.controlGrant = null;
    this.inputSeq = 0;
    if (hadGrant && viewer) {
      await this.options.api
        .revokeMirrorControlGrant(viewer.viewerId)
        .catch(() => undefined);
    }
    if (!this.disposed) this.publishControlState("inactive", reason);
  }

  async stopControl(): Promise<void> {
    await this.endControl();
  }

  sendInput(input: MirrorControlInput): void {
    const grant = this.controlGrant;
    const channel = this.inputChannel;
    if (!grant || !channel || !this.inputOpen || this.disposed) return;
    const message = {
      kind: input.kind,
      grant_id: grant.grantId,
      gesture_id: input.gestureId,
      seq: ++this.inputSeq,
      native_epoch: grant.nativeEpoch,
      display_generation: grant.sourceGeneration,
      geometry_revision: this.geometryRevision ?? 0,
      ...(input.pointer
        ? {
            pointer: {
              x: input.pointer.x,
              y: input.pointer.y,
              ...(input.pointer.button
                ? { button: input.pointer.button }
                : {}),
              ...(input.pointer.deltaX !== undefined
                ? { delta_x: input.pointer.deltaX }
                : {}),
              ...(input.pointer.deltaY !== undefined
                ? { delta_y: input.pointer.deltaY }
                : {}),
            },
          }
        : {}),
      ...(input.key
        ? {
            key: {
              key: input.key.key,
              ...(input.key.modifiers?.length
                ? { modifiers: [...input.key.modifiers] }
                : {}),
            },
          }
        : {}),
      ...(input.text ? { text: input.text } : {}),
    };
    channel.send(new TextEncoder().encode(JSON.stringify(message)));
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
      this.controlChannel = control;
      control.onopen = () => {
        this.controlOpen = true;
        this.publishStream();
      };
      control.onmessage = (event: MessageEvent<unknown>) =>
        (this.authorizationMessage(event.data), this.controlMessage(event.data));
      control.onclose = () => {
        this.controlOpen = false;
        this.fail(new MirrorVideoError("viewer_revoked"));
      };
      control.onerror = () => this.fail(new MirrorVideoError("transport"));
      this.inputChannel = this.wireInputChannel(peer);
      this.voiceChannel = peer.createDataChannel("mirror-voice", { ordered: true });
      this.voiceChannel.onmessage = (event: MessageEvent<unknown>) => this.voiceMessage(event.data);
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
    clearTimeout(this.controlRenewal);
    if (this.controlGrant) await this.endControl();
    this.inputChannel?.close();
    this.inputChannel = null;
    this.inputOpen = false;
    this.peer?.close();
    this.stream?.getTracks().forEach((track) => track.stop());
    this.options.callbacks.stream(null);
    this.options.callbacks.metadata(null);
    await this.starting;
    await this.closeRemote();
  }
}
