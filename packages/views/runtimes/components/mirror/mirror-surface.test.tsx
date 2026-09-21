// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { RuntimeDevice, VscreenScope } from "@multica/core/types";
import { RESOURCES } from "../../../test/i18n";
import { MirrorSurface } from "./mirror-surface";

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
    backendIdentity: "https://fixture.invalid",
    workspaceId: "workspace",
    runtimeId: "runtime",
    uid: 501,
  },
  source: { kind: "virtual", sourceId: "display:1" },
  nativeEpoch: "native",
  generation: "display",
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

const stopControl = vi.fn(async () => undefined);
const startControl = vi.fn(async () => undefined);
const invalidateQueries = vi.fn(async () => undefined);
const commandMutate = vi.fn((_command, options) => {
  void options?.onSuccess?.();
});
let controlBarProps: {
  onCommand: (kind: "emergency_stop" | "enable_interaction") => void;
  onAgentChange: (agentId: string) => void;
};

vi.mock("@multica/core/workspace", () => ({
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: async () => [{ id: "agent", name: "Mirror agent", runtime_id: "runtime" }] }),
}));
vi.mock("@multica/core/chat/queries", () => ({
  chatSessionsOptions: () => ({ queryKey: ["sessions"], queryFn: async () => [{ id: "chat", agent_id: "agent", status: "active", updated_at: "2026-09-21" }] }),
  chatMessagesOptions: () => ({ queryKey: ["messages"], queryFn: async () => [] }),
  pendingChatTaskOptions: () => ({ queryKey: ["pending-task"], queryFn: async () => null }),
}));
vi.mock("../../../chat/components/chat-message-list", () => ({
  ChatMessageList: () => <div>Conversation content</div>,
}));

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQueryClient: () =>
      ({ invalidateQueries }) as unknown as ReturnType<typeof actual.useQueryClient>,
  };
});

vi.mock("@multica/core/api", () => ({
  vscreenErrorReason: () => null,
}));

vi.mock("@multica/core/runtimes", () => ({
  deriveVscreenAccess: () => ({ canRequestTakeover: false }),
  runtimeDisplayLabel: () => "Fixture runtime",
  useVscreenCommand: () => ({ mutate: commandMutate, isPending: false }),
  vscreenCommandOptions: () => ({
    queryKey: ["command"],
    queryFn: async () => null,
    enabled: false,
  }),
  vscreenKeys: { all: () => ["vscreen"] },
  vscreenStateOptions: () => ({
    queryKey: ["state"],
    queryFn: async () => ({
      workspaceId: "workspace",
      runtimeId: "runtime",
      state: {
        state: "ready",
        permissions: { screenRecording: "granted", accessibility: "granted" },
        humanInteraction: true,
      },
    }),
  }),
  vscreenSourcesOptions: () => ({
    queryKey: ["sources"],
    queryFn: async () => ({ sources: [source] }),
  }),
}));

vi.mock("./use-video-session", () => ({
  useVideoSession: () => ({
    stream: null,
    state: "streaming",
    control: { status: "inactive" },
    reason: null,
    quality: null,
    metadata: null,
    startControl,
    stopControl,
    sendInput: vi.fn(),
    onFrame: vi.fn(),
  }),
}));

vi.mock("./mirror-platform", () => ({
  useMirrorPlatform: () => ({ openFloating: undefined }),
}));

vi.mock("./mirror-handoff", () => ({ MirrorHandoff: () => null }));
vi.mock("./mirror-video", () => ({ MirrorVideo: () => null }));
vi.mock("./interactive-mirror-video", () => ({
  InteractiveMirrorVideo: () => null,
}));
vi.mock("./mirror-source-picker", () => ({
  MirrorSourcePicker: () => null,
  mirrorSourceKey: () => "display:1",
}));
vi.mock("./mirror-command-controls", () => ({
  MirrorCommandControls: () => null,
}));
vi.mock("./mirror-controller-presence", () => ({
  MirrorControllerPresence: () => null,
}));
vi.mock("./mirror-control-bar", () => ({
  MirrorControlBar: (props: typeof controlBarProps) => {
    controlBarProps = props;
    return null;
  },
}));

describe("MirrorSurface emergency stop", () => {
  it("leaves local control mode after the host command is accepted", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <I18nProvider locale="en" resources={RESOURCES}>
        <QueryClientProvider client={queryClient}>
          {children}
        </QueryClientProvider>
      </I18nProvider>
    );

    render(<MirrorSurface scope={scope} runtime={runtime} />, { wrapper });

    await waitFor(() => expect(controlBarProps).toBeDefined());
    controlBarProps.onCommand("emergency_stop");

    await waitFor(() => expect(stopControl).toHaveBeenCalledOnce());
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["vscreen"],
    });
  });
});

describe("MirrorSurface interaction", () => {
  it("toggles the video conversation overlay without deleting its session", async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={queryClient}>
        <MirrorSurface scope={scope} runtime={runtime} />
      </QueryClientProvider>
    </I18nProvider>);
    await waitFor(() => expect(queryClient.getQueryData(["sessions"])).toBeDefined());
    act(() => controlBarProps.onAgentChange("agent"));
    const overlay = await screen.findByRole("complementary", { name: "Live conversation" });
    expect(overlay.parentElement).toHaveClass("aspect-video");
    expect(overlay).toHaveClass("absolute", "bottom-3", "right-3");
    fireEvent.click(screen.getByRole("button", { name: "Clear screen" }));
    expect(screen.queryByRole("complementary")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Show chat" })).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(screen.getByRole("button", { name: "Show chat" }));
    expect(screen.getByText("Conversation content")).toBeInTheDocument();
    expect(queryClient.getQueryData(["sessions"])).toHaveLength(1);
    act(() => controlBarProps.onAgentChange(""));
    expect(screen.queryByRole("complementary")).not.toBeInTheDocument();
  });

  it("starts local screen control after enabling remote interaction", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <I18nProvider locale="en" resources={RESOURCES}>
        <QueryClientProvider client={queryClient}>
          {children}
        </QueryClientProvider>
      </I18nProvider>
    );

    render(<MirrorSurface scope={scope} runtime={runtime} />, { wrapper });

    await waitFor(() => expect(controlBarProps).toBeDefined());
    controlBarProps.onCommand("enable_interaction");

    await waitFor(() => expect(startControl).toHaveBeenCalledOnce());
  });
});
