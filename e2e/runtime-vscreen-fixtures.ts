import type { Page, Route } from "@playwright/test";
import type { RuntimeDevice } from "../packages/core/types/agent";

export const RUNTIME_ID = "88888888-8888-4888-8888-888888888888";
export const PHYSICAL_LABEL = "Physical display · UI fixture physical";
export const SYSTEM_LABEL = "System display · UI fixture system";
export const PHYSICAL_PIXEL = [30, 100, 220];
export const SYSTEM_PIXEL = [220, 120, 30];

type SourceKind = "physical" | "system";
interface SourceWire {
  resource: {
    backend_identity: string;
    workspace_id: string;
    runtime_id: string;
    uid: number;
  };
  source: { kind: SourceKind; source_id: string };
  native_epoch: string;
  generation: string;
  primary: boolean;
  name: string;
  width: number;
  height: number;
  scale: number;
  geometry_revision: number;
}
interface SessionRequest {
  viewer_id: string;
  source: SourceWire["source"];
  source_generation: string;
  protocol_version: number;
  transport: string;
}

// Synthetic canvas video tests the shipped browser page, not native capture,
// H.264 transport, display permissions, or the agent-desktop runtime.
async function installSyntheticRTC(page: Page) {
  await page.addInitScript(
    ({ physicalPixel, systemPixel }) => {
      class SyntheticPeer extends EventTarget {
        iceGatheringState = "complete";
        localDescription: RTCSessionDescriptionInit | null = null;
        ontrack:
          | ((event: {
              track: MediaStreamTrack;
              streams: MediaStream[];
            }) => void)
          | null = null;
        channel = {
          onopen: null as (() => void) | null,
          onmessage: null as ((event: MessageEvent) => void) | null,
        };
        stream: MediaStream | null = null;
        timer: number | undefined;
        addTransceiver() {}
        createDataChannel() {
          return this.channel;
        }
        async createOffer() {
          return { type: "offer" as const, sdp: "synthetic-ui-offer" };
        }
        async setLocalDescription(offer: RTCSessionDescriptionInit) {
          this.localDescription = offer;
        }
        async setRemoteDescription(answer: RTCSessionDescriptionInit) {
          const metadata = JSON.parse(answer.sdp ?? "");
          const canvas = document.createElement("canvas");
          canvas.width = 160;
          canvas.height = 90;
          const context = canvas.getContext("2d");
          if (!context) throw new Error("Synthetic video needs a canvas context");
          const pixel =
            metadata.source_binding.source.kind === "system"
              ? systemPixel
              : physicalPixel;
          const draw = () => {
            context.fillStyle = `rgb(${pixel.join(",")})`;
            context.fillRect(0, 0, canvas.width, canvas.height);
          };
          draw();
          this.stream = canvas.captureStream(10);
          this.timer = window.setInterval(draw, 100);
          this.channel.onopen?.();
          this.channel.onmessage?.(
            new MessageEvent("message", { data: JSON.stringify(metadata) }),
          );
          this.ontrack?.({
            track: this.stream.getVideoTracks()[0],
            streams: [this.stream],
          });
        }
        close() {
          window.clearInterval(this.timer);
          this.stream?.getTracks().forEach((track) => track.stop());
        }
      }
      Object.defineProperty(window, "RTCPeerConnection", {
        value: SyntheticPeer,
      });
    },
    { physicalPixel: PHYSICAL_PIXEL, systemPixel: SYSTEM_PIXEL },
  );
}

export async function mockMirrorBrowserUI(
  page: Page,
  identity: { workspaceId: string; userId: string },
  options: {
    readerOnly?: boolean;
    runtimeName?: string;
  } = {},
) {
  await installSyntheticRTC(page);
  const runtime: RuntimeDevice = {
    id: RUNTIME_ID,
    workspace_id: identity.workspaceId,
    daemon_id: "ui-fixture-daemon",
    owner_id: options.readerOnly
      ? "99999999-9999-4999-8999-999999999999"
      : identity.userId,
    visibility: "public",
    name: options.runtimeName ?? "Browser UI fixture runtime",
    runtime_mode: "local",
    provider: "codex",
    launch_header: "",
    device_info: "Browser UI fixture",
    status: "online",
    metadata: {
      capabilities: ["screen-mirror-video-v2", "mirror-viewer-grant-v1"],
    },
    last_seen_at: "2026-09-01T00:00:00Z",
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-01T00:00:00Z",
  };
  const envelope = {
    workspace_id: identity.workspaceId,
    runtime_id: RUNTIME_ID,
    daemon_generation: "ui-daemon-epoch",
    request_id: "ui-query",
  };
  const fixture = {
    sources: ["physical", "system"] as SourceKind[],
    permission: "granted" as "granted" | "denied",
    holdNextSource: null as SourceKind | null,
    held: null as { route: Route; body: unknown; sessionId: string } | null,
    created: [] as { id: string; request: SessionRequest }[],
    closed: [] as string[],
    polled: [] as string[],
    sourceReads: 0,
    async releaseHeld() {
      const held = fixture.held;
      if (!held) throw new Error("No delayed browser UI response to release");
      fixture.held = null;
      await held.route.fulfill({ json: held.body });
      return held.sessionId;
    },
  };
  const sessions = new Map<string, { viewer_grant: Record<string, unknown> }>();
  await page.route("**/api/runtimes**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path.endsWith("/api/runtimes") && request.method() === "GET") {
      await route.fulfill({ json: [runtime] });
      return;
    }
    const root = `/api/runtimes/${RUNTIME_ID}`;
    if (!path.includes(root)) return route.fallback();
    const suffix = path.slice(path.indexOf(root) + root.length);
    const backendIdentity = url.origin + path.slice(0, path.indexOf("/api/runtimes"));
    const source = (kind: SourceKind): SourceWire => ({
      resource: {
        backend_identity: backendIdentity,
        workspace_id: identity.workspaceId,
        runtime_id: RUNTIME_ID,
        uid: 501,
      },
      source: { kind, source_id: `ui-${kind}` },
      native_epoch: "ui-native-epoch",
      generation: `ui-${kind}-generation`,
      primary: kind === "physical",
      name: `UI fixture ${kind}`,
      width: 1280,
      height: 720,
      scale: 1,
      geometry_revision: 1,
    });
    if (suffix === "/mirror/sources") {
      fixture.sourceReads++;
      if (fixture.permission === "denied") {
        return route.fulfill({
          status: 409,
          json: { reason: "permission_denied" },
        });
      }
      return route.fulfill({
        json: { ...envelope, sources: fixture.sources.map(source) },
      });
    }
    if (suffix === "/mirror/config") {
      return route.fulfill({
        json: { ice_servers: [], turn_configured: false },
      });
    }
    if (suffix === "/mirror/sessions" && request.method() === "POST") {
      const input: SessionRequest = request.postDataJSON();
      if (
        !["physical", "system"].includes(input.source.kind) ||
        input.protocol_version !== 2 ||
        input.transport !== "video"
      ) {
        throw new Error("Unexpected browser mirror contract");
      }
      const binding = source(input.source.kind);
      if (
        input.source.source_id !== binding.source.source_id ||
        input.source_generation !== binding.generation
      ) {
        throw new Error("Browser requested an unknown source binding");
      }
      const id = `ui-session-${fixture.created.length + 1}`;
      fixture.created.push({ id, request: input });
      const expires = new Date(Date.now() + 30_000).toISOString();
      const quality = {
        width: 1280,
        height: 720,
        fps: 10,
        bitrate: 1_000_000,
        max_level_idc: 31,
      };
      const body = {
        id,
        workspace_id: identity.workspaceId,
        runtime_id: RUNTIME_ID,
        user_id: identity.userId,
        daemon_id: runtime.daemon_id,
        viewer_id: input.viewer_id,
        created_at: new Date().toISOString(),
        expires_at: expires,
        state: "answered",
        ice_config: { ice_servers: [], turn_configured: false },
        viewer_grant: {
          grant_id: `${id}-grant`,
          session_id: id,
          workspace_id: identity.workspaceId,
          runtime_id: RUNTIME_ID,
          user_id: identity.userId,
          viewer_id: input.viewer_id,
          native_epoch: binding.native_epoch,
          source: binding.source,
          source_generation: binding.generation,
          expires_at: expires,
        },
        video_quality: quality,
        answer: {
          type: "answer",
          sdp: JSON.stringify({
            type: "mirror:video-meta",
            source_binding: binding,
            geometry_revision: 1,
            pts_nanos: 1,
            quality,
          }),
        },
      };
      sessions.set(id, body);
      if (fixture.holdNextSource === input.source.kind) {
        fixture.holdNextSource = null;
        fixture.held = { route, body, sessionId: id };
        return;
      }
      return route.fulfill({ json: body });
    }
    const sessionId = suffix.match(/^\/mirror\/sessions\/([^/]+)$/)?.[1];
    if (sessionId && request.method() === "DELETE") {
      fixture.closed.push(sessionId);
      return route.fulfill({ status: 204 });
    }
    if (sessionId && request.method() === "GET") {
      fixture.polled.push(sessionId);
      return route.fulfill({ json: sessions.get(sessionId) });
    }
    const renewalId = suffix.match(/^\/mirror\/sessions\/([^/]+)\/renew$/)?.[1];
    if (renewalId) {
      const session = sessions.get(renewalId);
      if (!session) throw new Error("Unknown synthetic viewer");
      return route.fulfill({
        json: {
          ...session.viewer_grant,
          expires_at: new Date(Date.now() + 30_000).toISOString(),
        },
      });
    }
    return route.fulfill({
      status: 404,
      json: { reason: "unknown_ui_fixture_route" },
    });
  });
  return fixture;
}

export async function visibleVideoPixel(page: Page) {
  return page
    .locator('section[aria-label="Runtime screen"] video')
    .evaluate((video: HTMLVideoElement) => {
      if (video.readyState < 2) return null;
      const canvas = document.createElement("canvas");
      canvas.width = canvas.height = 1;
      const context = canvas.getContext("2d");
      if (!context) throw new Error("Video assertion needs a canvas context");
      context.drawImage(video, 0, 0, 1, 1);
      return Array.from(context.getImageData(0, 0, 1, 1).data).slice(0, 3);
    });
}
