// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { RuntimeDevice, VscreenScope } from "@multica/core/types";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { getTestResources } from "../../../test/i18n";
import { MirrorSurface, mirrorConnectionStage } from "./mirror-surface";

const scope: VscreenScope = {
  backendIdentity: "https://fixture.invalid",
  accountId: "owner",
  workspaceId: "workspace",
  runtimeId: "runtime",
};

const runtime: RuntimeDevice = {
  id: "runtime",
  workspace_id: "workspace",
  owner_id: "owner",
  visibility: "private",
  status: "online",
  daemon_id: "daemon",
  name: "Fixture runtime",
  runtime_mode: "local",
  provider: "fixture",
  launch_header: "",
  device_info: "",
  metadata: { capabilities: [] },
  last_seen_at: null,
  created_at: "2026-09-19T00:00:00Z",
  updated_at: "2026-09-19T00:00:00Z",
};

const source = {
  resource: {
    backendIdentity: scope.backendIdentity,
    workspaceId: scope.workspaceId,
    runtimeId: scope.runtimeId,
    uid: 501,
  },
  source: { kind: "physical" as const, sourceId: "display:1" },
  nativeEpoch: "native",
  generation: "display",
  primary: true,
  name: "Display 1",
  width: 1600,
  height: 900,
  logicalWidth: 1600,
  logicalHeight: 900,
  scale: 1,
  x: 0,
  y: 0,
  geometryRevision: 1,
};

const sourceQuery = vi.fn(async () => ({ sources: [source] }));
vi.mock("@multica/core/api", () => ({ vscreenErrorReason: () => null }));
vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ role: "owner" }),
}));
vi.mock("@multica/core/runtimes", () => ({
  runtimeDisplayLabel: () => "Fixture runtime",
  vscreenSourcesOptions: () => ({
    queryKey: ["sources"],
    queryFn: sourceQuery,
  }),
  agentDesktopReadinessOptions: () => ({
    queryKey: ["agent-desktop"],
    queryFn: async () => ({
      platform: "darwin",
      version: "1",
      permissions: {
        accessibility: "granted",
        screenRecording: "granted",
        automation: "not_required",
      },
      surfaceCount: 1,
      ready: true,
    }),
  }),
}));
vi.mock("./use-video-session", () => ({
  useVideoSession: () => ({
    stream: null,
    state: "streaming",
    reason: null,
    quality: {
      width: 1600,
      height: 900,
      fps: 30,
      bitrate: 4_000_000,
      maxLevelIdc: 31,
    },
    metadata: null,
    statistics: {
      framesPerSecond: 24,
      bitsPerSecond: 2_000_000,
      packetLossPercent: 0.5,
      roundTripTimeMs: 25,
    },
    onFrame: vi.fn(),
    retryNow: vi.fn(),
    cancelAutoRetry: vi.fn(),
    retryAt: null,
    retryAttempt: 0,
  }),
}));
let platformOpenFloating:
  | ((scope: VscreenScope, title: string) => Promise<void>)
  | undefined;
vi.mock("./mirror-platform", () => ({
  useMirrorPlatform: () => ({ openFloating: platformOpenFloating }),
}));
vi.mock("./mirror-video", () => ({ MirrorVideo: () => null }));
const mirrorControl = vi.hoisted(() => ({
  status: { state: "view_only" } as Record<string, unknown>,
  pending: false,
  error: false,
  leaseSeconds: null as number | null,
  request: vi.fn(),
  approve: vi.fn(),
  deny: vi.fn(),
  send: vi.fn(),
  stop: vi.fn(),
  refetch: vi.fn(),
}));
vi.mock("./use-mirror-control", () => ({
  useMirrorControl: () => mirrorControl,
}));

function renderSurface(compact = false) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <I18nProvider locale="en" resources={getTestResources()}>
      <QueryClientProvider client={queryClient}>
        <MirrorSurface scope={scope} runtime={runtime} compact={compact} />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return queryClient;
}

describe("MirrorSurface canvas-first viewing", () => {
  beforeEach(() => {
    mirrorControl.status = { state: "view_only" };
    for (const method of [
      mirrorControl.request,
      mirrorControl.approve,
      mirrorControl.deny,
      mirrorControl.send,
      mirrorControl.stop,
      mirrorControl.refetch,
    ]) method.mockClear();
  });
  it("only reaches the streaming stage after frames are decoded", () => {
    const base = {
      readable: true,
      online: true,
      sourcesPending: false,
      hasSource: true,
      reason: null,
    } as const;
    expect(mirrorConnectionStage({ ...base, videoState: "closed" })).toBe(2);
    expect(mirrorConnectionStage({ ...base, videoState: "failed" })).toBe(2);
    expect(mirrorConnectionStage({ ...base, videoState: "negotiating" })).toBe(3);
    expect(mirrorConnectionStage({ ...base, videoState: "decoding" })).toBe(4);
    expect(mirrorConnectionStage({ ...base, videoState: "streaming" })).toBe(5);
  });
  it("shows measured receive statistics and starts an explicit control request", async () => {
    renderSurface();
    expect(
      await screen.findByText(
        "1600 × 900 · 24 FPS · 2.0 Mbps · 25 ms RTT · 0.5% loss",
      ),
    ).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Mirror inspector" }),
    );
    fireEvent.click(screen.getByRole("tab", { name: "Control" }));
    const requestButtons = screen.getAllByRole("button", { name: "Request control" });
    expect(requestButtons.length).toBeGreaterThan(0);
    fireEvent.click(requestButtons[0]!);
    expect(mirrorControl.request).toHaveBeenCalledOnce();
    expect(screen.getAllByRole("status")).toHaveLength(1);
  });

  it("keeps fullscreen and floating-window actions operable", async () => {
    const originalRequestFullscreen = HTMLElement.prototype.requestFullscreen;
    const originalExitFullscreen = Object.getOwnPropertyDescriptor(
      document,
      "exitFullscreen",
    );
    const originalFullscreenElement = Object.getOwnPropertyDescriptor(
      document,
      "fullscreenElement",
    );
    let fullscreenElement: Element | null = null;
    const requestFullscreen = vi.fn(async () => {
      fullscreenElement = screen.getByRole("region", { name: "Runtime screen" });
      document.dispatchEvent(new Event("fullscreenchange"));
    });
    const exitFullscreen = vi.fn(async () => {
      fullscreenElement = null;
      document.dispatchEvent(new Event("fullscreenchange"));
    });
    Object.defineProperty(HTMLElement.prototype, "requestFullscreen", {
      configurable: true,
      value: requestFullscreen,
    });
    Object.defineProperty(document, "fullscreenElement", {
      configurable: true,
      get: () => fullscreenElement,
    });
    Object.defineProperty(document, "exitFullscreen", {
      configurable: true,
      value: exitFullscreen,
    });
    platformOpenFloating = vi.fn(async () => undefined);
    try {
      renderSurface();
      fireEvent.click(
        await screen.findByRole("button", { name: "Enter full screen" }),
      );
      expect(requestFullscreen).toHaveBeenCalledOnce();
      await screen.findByRole("button", { name: "Exit full screen" });
      await act(async () =>
        fireEvent.click(
          screen.getByRole("button", { name: "Open presentation view" }),
        ),
      );
      expect(platformOpenFloating).toHaveBeenCalledWith(
        scope,
        "Fixture runtime",
      );
      fireEvent.click(screen.getByRole("button", { name: "Exit full screen" }));
      expect(exitFullscreen).toHaveBeenCalledOnce();
    } finally {
      Object.defineProperty(
        HTMLElement.prototype,
        "requestFullscreen",
        originalRequestFullscreen
          ? { configurable: true, value: originalRequestFullscreen }
          : { configurable: true, value: undefined },
      );
      if (originalExitFullscreen)
        Object.defineProperty(
          document,
          "exitFullscreen",
          originalExitFullscreen,
        );
      if (originalFullscreenElement)
        Object.defineProperty(
          document,
          "fullscreenElement",
          originalFullscreenElement,
        );
    }
  });

  it("releases human control when Escape is pressed", async () => {
    mirrorControl.status = {
      state: "controlling",
      controlId: "control-1",
      controllerId: "owner",
      leaseExpiresAt: null,
      leaseMaxExpiresAt: null,
    };
    renderSurface();
    await waitFor(() => expect(screen.getByText("You are controlling")).toBeInTheDocument());
    await screen.findByRole("button", { name: "Stop control" });
    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(mirrorControl.stop).toHaveBeenCalledOnce());
  });

  it("lets a runtime owner force-end another controller's lease", async () => {
    mirrorControl.status = {
      state: "other_controller",
      controlId: "control-2",
      controllerId: "Alex",
      leaseExpiresAt: null,
      leaseMaxExpiresAt: null,
    };
    renderSurface();
    expect(await screen.findByText("Alex is controlling Display 1")).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Mirror inspector" }),
    );
    fireEvent.click(screen.getByRole("tab", { name: "Control" }));
    const takeBackButtons = screen.getAllByRole("button", {
      name: "Take back control",
    });
    expect(takeBackButtons.length).toBeGreaterThan(0);
    fireEvent.click(takeBackButtons.at(-1)!);
    expect(mirrorControl.stop).toHaveBeenCalledOnce();
  });

  it("keeps refresh in compact mode and omits floating-window actions", async () => {
    const queryClient = renderSurface(true);
    await screen.findAllByRole("button", { name: "Reconnect" });
    expect(
      screen.queryByRole("button", { name: "Open presentation view" }),
    ).toBeNull();
    expect(screen.getByRole("button", { name: "Screen source" })).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "Reconnect" })[0]!);
    expect(queryClient.getQueryState(["sources"])?.fetchStatus).not.toBe(
      "idle",
    );
  });
});
