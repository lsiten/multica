import type { Page, Route } from "@playwright/test";
import type { RuntimeDevice } from "../packages/core/types/agent";
import type { VscreenSourceDescriptorSchema } from "../packages/core/api/vscreen-schemas";

export const RUNTIME_ID = "88888888-8888-4888-8888-888888888888";
export const SOURCE_TASK_ID = "11111111-1111-4111-8111-111111111111";
export const CHILD_TASK_ID = "22222222-2222-4222-8222-222222222222";
export const VIRTUAL_LABEL = "Virtual screen · UI fixture virtual";
export const PHYSICAL_LABEL = "Physical display · UI fixture physical";
export const VIRTUAL_PIXEL = [20, 160, 80];
export const PHYSICAL_PIXEL = [30, 100, 220];

type SourceWire = typeof VscreenSourceDescriptorSchema["_input"];
type SourceKind = "virtual" | "physical";
interface SessionRequest {
  viewer_id: string;
  source: SourceWire["source"];
  source_generation: string;
  protocol_version: number;
  transport: string;
}
interface InterventionWire {
  id: string;
  workspace_id: string;
  runtime_id: string;
  agent_id: string;
  source_task_id: string;
  state: string;
  reason: string;
  human_summary: string;
  return_receipt_id: string;
  continuation_task_id: string | null;
  version: number;
}

// Synthetic canvas video tests the shipped browser page, not native capture,
// H.264 transport, App control, display creation, or permission authorization.
async function installSyntheticRTC(page: Page) {
  await page.addInitScript(({ virtualPixel, physicalPixel }) => {
    class SyntheticPeer extends EventTarget {
      iceGatheringState = "complete";
      localDescription: RTCSessionDescriptionInit | null = null;
      ontrack: ((event: { track: MediaStreamTrack; streams: MediaStream[] }) => void) | null = null;
      channel = {
        onopen: null as (() => void) | null,
        onmessage: null as ((event: MessageEvent) => void) | null,
      };
      stream: MediaStream | null = null;
      timer: number | undefined;
      addTransceiver() {}
      createDataChannel() { return this.channel; }
      async createOffer() { return { type: "offer" as const, sdp: "synthetic-ui-offer" }; }
      async setLocalDescription(offer: RTCSessionDescriptionInit) { this.localDescription = offer; }
      async setRemoteDescription(answer: RTCSessionDescriptionInit) {
        const metadata = JSON.parse(answer.sdp ?? "");
        const canvas = document.createElement("canvas");
        canvas.width = 160;
        canvas.height = 90;
        const context = canvas.getContext("2d");
        if (!context) throw new Error("Synthetic video needs a canvas context");
        const pixel = metadata.source_binding.source.kind === "virtual" ? virtualPixel : physicalPixel;
        const draw = () => {
          context.fillStyle = `rgb(${pixel.join(",")})`;
          context.fillRect(0, 0, canvas.width, canvas.height);
        };
        draw();
        this.stream = canvas.captureStream(10);
        this.timer = window.setInterval(draw, 100);
        this.channel.onopen?.();
        this.channel.onmessage?.(new MessageEvent("message", { data: JSON.stringify(metadata) }));
        this.ontrack?.({ track: this.stream.getVideoTracks()[0], streams: [this.stream] });
      }
      close() {
        window.clearInterval(this.timer);
        this.stream?.getTracks().forEach((track) => track.stop());
      }
    }
    Object.defineProperty(window, "RTCPeerConnection", { value: SyntheticPeer });
  }, { virtualPixel: VIRTUAL_PIXEL, physicalPixel: PHYSICAL_PIXEL });
}

export async function mockVscreenBrowserUI(
  page: Page,
  identity: { workspaceId: string; userId: string },
  options: {
    disabled?: boolean;
    readerOnly?: boolean;
    runtimeName?: string;
    handoff?: "request" | "ready_to_continue";
  } = {},
) {
  await installSyntheticRTC(page);
  const runtime: RuntimeDevice = {
    id: RUNTIME_ID,
    workspace_id: identity.workspaceId,
    daemon_id: "ui-fixture-daemon",
    owner_id: options.readerOnly ? "99999999-9999-4999-8999-999999999999" : identity.userId,
    visibility: "public",
    name: options.runtimeName ?? "Browser UI fixture runtime",
    runtime_mode: "local",
    provider: "codex",
    launch_header: "",
    device_info: "Browser UI fixture",
    status: "online",
    metadata: { capabilities: ["virtual-screen-v1", "screen-mirror-video-v2", "mirror-viewer-grant-v1"] },
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
  const intervention: InterventionWire = {
    id: "ui-intervention",
    workspace_id: identity.workspaceId,
    runtime_id: RUNTIME_ID,
    agent_id: "ui-agent",
    source_task_id: SOURCE_TASK_ID,
    state: options.handoff === "ready_to_continue" ? "ready_to_continue" : "awaiting_takeover",
    reason: "background_action_unsupported",
    human_summary: "",
    return_receipt_id: options.handoff === "ready_to_continue" ? "ui-return-receipt" : "",
    continuation_task_id: null,
    version: 1,
  };
  const fixture = {
    sources: (options.disabled ? ["physical"] : ["virtual", "physical"]) as SourceKind[],
    permission: "granted" as "granted" | "denied",
    stateRevision: 1,
    holdNextSource: null as SourceKind | null,
    held: null as { route: Route; body: unknown; sessionId: string } | null,
    created: [] as { id: string; request: SessionRequest }[],
    closed: [] as string[],
    polled: [] as string[],
    commands: [] as unknown[],
    interventions: options.handoff === "ready_to_continue" ? [intervention] : [] as InterventionWire[],
    continuations: [] as { human_summary: string; fresh_session: boolean }[],
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
  const commands = new Map<string, object>();
  await page.route(`**/api/tasks/${SOURCE_TASK_ID}/vscreen/interventions/**`, async (route) => {
    if (!route.request().url().endsWith("/ui-intervention/continue")) {
      return route.fulfill({ status: 404, json: { reason: "unknown_ui_fixture_route" } });
    }
    const body = route.request().postDataJSON();
    fixture.continuations.push(body);
    if (!body.fresh_session) {
      return route.fulfill({ status: 409, json: { reason: "resume_unavailable" } });
    }
    intervention.state = "continued";
    intervention.continuation_task_id = CHILD_TASK_ID;
    intervention.human_summary = body.human_summary;
    intervention.version++;
    return route.fulfill({ json: { task_id: CHILD_TASK_ID, intervention_id: intervention.id } });
  });
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
      resource: { backend_identity: backendIdentity, workspace_id: identity.workspaceId, runtime_id: RUNTIME_ID, uid: 501 },
      source: { kind, source_id: `ui-${kind}` },
      native_epoch: "ui-native-epoch",
      generation: `ui-${kind}-generation`,
      primary: kind === "physical",
      name: `UI fixture ${kind}`,
      width: 1280,
      height: 720,
      scale: 1,
    });
    if (suffix === "/vscreen") {
      const pendingHandoff = fixture.interventions[0]?.state === "awaiting_takeover";
      const idle = options.disabled || fixture.interventions[0]?.state === "ready_to_continue" || fixture.interventions[0]?.state === "continued";
      return route.fulfill({ json: { ...envelope, state: {
        runtime_id: RUNTIME_ID,
        state: options.disabled ? "disabled" : "ready",
        native_epoch: options.disabled ? "" : "ui-native-epoch",
        display_generation: options.disabled ? "" : "ui-virtual-generation",
        geometry_revision: options.disabled ? 0 : 1,
        control_state: pendingHandoff ? "awaiting_takeover" : idle ? "idle" : "agent",
        active_task_id: idle ? null : SOURCE_TASK_ID,
        intervention_id: fixture.interventions[0]?.id ?? null,
        permissions: { screen_recording: fixture.permission, accessibility: "granted" },
        state_revision: fixture.stateRevision,
      } } });
    }
    if (suffix === "/vscreen/interventions") {
      return route.fulfill({ json: fixture.interventions });
    }
    if (suffix === "/mirror/sources") {
      fixture.sourceReads++;
      return route.fulfill({ json: { ...envelope, sources: fixture.sources.map(source) } });
    }
    if (suffix === "/mirror/config") {
      return route.fulfill({ json: { ice_servers: [], turn_configured: false } });
    }
    if (suffix === "/vscreen/commands") {
      const input = request.postDataJSON();
      fixture.commands.push(input);
      if (options.handoff === "request" && input.kind === "request_takeover") {
        fixture.interventions = [intervention];
        fixture.stateRevision++;
        const receipt = { ...envelope, command_id: input.command_id, receipt_id: "ui-command-receipt", state: "succeeded" };
        commands.set(input.command_id, receipt);
        return route.fulfill({ json: receipt });
      }
      return route.fulfill({ status: 403, json: { reason: "permission_denied" } });
    }
    const commandId = suffix.match(/^\/vscreen\/commands\/([^/]+)$/)?.[1];
    if (commandId && commands.has(commandId)) {
      return route.fulfill({ json: commands.get(commandId) });
    }
    if (suffix === "/mirror/sessions" && request.method() === "POST") {
      const input: SessionRequest = request.postDataJSON();
      if (!["virtual", "physical"].includes(input.source.kind) || input.protocol_version !== 2 || input.transport !== "video") {
        throw new Error("Unexpected browser mirror contract");
      }
      const binding = source(input.source.kind as SourceKind);
      if (input.source.source_id !== binding.source.source_id || input.source_generation !== binding.generation) {
        throw new Error("Browser requested an unknown source binding");
      }
      const id = `ui-session-${fixture.created.length + 1}`;
      fixture.created.push({ id, request: input });
      const expires = new Date(Date.now() + 30_000).toISOString();
      const quality = { width: 1280, height: 720, fps: 10, bitrate: 1_000_000, max_level_idc: 31 };
      const body = {
        id, workspace_id: identity.workspaceId, runtime_id: RUNTIME_ID, user_id: identity.userId,
        daemon_id: runtime.daemon_id, viewer_id: input.viewer_id,
        created_at: new Date().toISOString(), expires_at: expires, state: "answered",
        ice_config: { ice_servers: [], turn_configured: false },
        viewer_grant: {
          grant_id: `${id}-grant`, session_id: id, workspace_id: identity.workspaceId,
          runtime_id: RUNTIME_ID, user_id: identity.userId, viewer_id: input.viewer_id,
          native_epoch: binding.native_epoch, source: binding.source,
          source_generation: binding.generation, expires_at: expires,
        },
        video_quality: quality,
        answer: { type: "answer", sdp: JSON.stringify({ type: "mirror:video-meta", source_binding: binding, geometry_revision: 1, pts_nanos: 1, quality }) },
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
      return route.fulfill({ json: { ...session.viewer_grant, expires_at: new Date(Date.now() + 30_000).toISOString() } });
    }
    // Never let a fixture runtime operation reach a real daemon.
    return route.fulfill({ status: 404, json: { reason: "unknown_ui_fixture_route" } });
  });
  return fixture;
}

export async function visibleVideoPixel(page: Page) {
  return page.locator('section[aria-label="Runtime screen"] video').evaluate((video: HTMLVideoElement) => {
    if (video.readyState < 2) return null;
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = 1;
    const context = canvas.getContext("2d");
    if (!context) throw new Error("Video assertion needs a canvas context");
    context.drawImage(video, 0, 0, 1, 1);
    return Array.from(context.getImageData(0, 0, 1, 1).data).slice(0, 3);
  });
}
