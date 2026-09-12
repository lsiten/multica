// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { MachineMirrorAction } from "./runtime-mirror-action";

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    runtimeMirror: (id: string) => `/acme/runtimes/${id}/mirror`,
  }),
}));

vi.mock("../../navigation", () => ({
  AppLink: ({ children, href }: { children: ReactNode; href: string }) => (
    <a href={href}>{children}</a>
  ),
}));

function runtime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "runtime-1",
    workspace_id: "workspace-1",
    daemon_id: "daemon-1",
    name: "Codex (dev.local)",
    runtime_mode: "local",
    provider: "codex",
    launch_header: "",
    status: "online",
    device_info: "dev.local",
    metadata: { client_os: "macos", cli_version: "v0.1.19" },
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: null,
    created_at: "",
    updated_at: "",
    ...overrides,
  };
}

describe("MachineMirrorAction", () => {
  it("links to the shared mirror route from a supported local device", () => {
    renderWithI18n(
      <MachineMirrorAction
        runtimes={[
          runtime({
            metadata: {
              client_os: "macos",
              cli_version: "v0.1.20",
              capabilities: ["screen-mirror-v1"],
            },
          }),
        ]}
      />,
    );

    expect(screen.getByRole("link", { name: /Open mirror/i })).toHaveAttribute(
      "href",
      "/acme/runtimes/runtime-1/mirror",
    );
  });

  it("keeps the device-level action visible but disabled for a v0.1.19 CGO-free daemon", () => {
    renderWithI18n(<MachineMirrorAction runtimes={[runtime()]} />);

    const action = screen.getByRole("button", { name: /Open mirror/i });
    expect(action).toBeDisabled();
    expect(action).toHaveAttribute(
      "title",
      "Update the Multica daemon on this device to use screen mirroring.",
    );
    expect(
      screen.getByText(
        "Update the Multica daemon on this device to use screen mirroring.",
      ),
    ).toBeInTheDocument();
  });

  it("does not offer mirroring for cloud devices", () => {
    const { container } = renderWithI18n(
      <MachineMirrorAction
        runtimes={[runtime({ runtime_mode: "cloud", daemon_id: null })]}
      />,
    );

    expect(container).toBeEmptyDOMElement();
  });
});
