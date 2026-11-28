// @vitest-environment jsdom

import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AgentDesktopReadiness, AgentRuntime } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { AgentDesktopReadinessSection } from "./agent-desktop-readiness";

const transport = vi.hoisted(() => ({ check: vi.fn(), repair: vi.fn() }));
vi.mock("@multica/core/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@multica/core/api")>(),
  api: { getBaseUrl: () => "https://app.example.test" },
  getApi: () => ({ vscreen: () => ({
    getAgentDesktopReadiness: transport.check,
    openAgentDesktopPermissionGuide: transport.repair,
  }) }),
}));
vi.mock("@multica/core/auth", async (importOriginal) => {
  const state = { user: { id: "account-1" } };
  return {
    ...await importOriginal<typeof import("@multica/core/auth")>(),
    useAuthStore: Object.assign(
      (selector: (value: typeof state) => unknown) => selector(state),
      { getState: () => state },
    ),
  };
});

const denied: AgentDesktopReadiness = {
  platform: "macos", version: "1.3.1", surfaceCount: 1, ready: false,
  permissions: { accessibility: "denied", screenRecording: "granted", automation: "unknown" },
  reason: "accessibility_denied",
};
const runtime: AgentRuntime = {
  id: "runtime-1", workspace_id: "workspace-1", daemon_id: null, name: "Local runtime",
  runtime_mode: "local", provider: "", launch_header: "", status: "offline",
  device_info: "", owner_id: "account-1", visibility: "private", shared_with: [],
  last_seen_at: null, created_at: "", updated_at: "",
  metadata: { agent_desktop: {
    platform: "macos", version: "1.3.1", surface_count: 1, ready: false,
    permissions: { accessibility: "denied", screen_recording: "granted", automation: "unknown" },
    reason: "accessibility_denied",
  } },
};
const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.forEach((client) => client.clear());
  clients.length = 0;
  vi.resetAllMocks();
});

it("keeps cached denied permissions visible but blocks repair until the runtime reconnects", async () => {
  transport.check.mockResolvedValue(denied);
  transport.repair.mockResolvedValue({
    ...denied, ready: true, reason: undefined,
    permissions: { ...denied.permissions, accessibility: "granted" },
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  const surface = (status: AgentRuntime["status"]) => (
    <QueryClientProvider client={client}>
      <AgentDesktopReadinessSection canRepair runtime={{ ...runtime, status }} />
    </QueryClientProvider>
  );
  const view = renderWithI18n(surface("offline"));
  expect(screen.getByText("Allow Accessibility for the runtime.")).toBeInTheDocument();
  const repair = screen.getByRole("button", { name: "Open permission guide" });
  expect(repair).toBeDisabled();
  fireEvent.click(repair);
  expect(transport.repair).not.toHaveBeenCalled();
  expect(transport.check).not.toHaveBeenCalled();

  view.rerender(surface("online"));
  await waitFor(() => expect(repair).toBeEnabled());
  fireEvent.click(repair);

  await waitFor(() => expect(screen.queryByRole("button", { name: "Open permission guide" })).not.toBeInTheDocument());
  expect(screen.queryByText("Allow Accessibility for the runtime.")).not.toBeInTheDocument();
});
