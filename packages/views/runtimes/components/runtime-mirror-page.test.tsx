// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ButtonHTMLAttributes, ReactNode } from "react";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";
import { RuntimeMirrorPage } from "./runtime-mirror-page";

const mocks = vi.hoisted(() => ({
  createPin: vi.fn(),
  deletePin: vi.fn(),
  pins: { current: [] as Array<{ item_type: string; item_id: string }> },
  session: {
    current: {
      state: "idle",
      failureReason: null,
      imageUrl: null as string | null,
      turnConfigured: true,
    },
  },
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    runtimes: () => "/acme/runtimes",
    runtimeDetail: (id: string) => `/acme/runtimes/${id}`,
  }),
}));

vi.mock("@multica/core/runtimes", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/runtimes")>(
      "@multica/core/runtimes",
    );
  return {
    ...actual,
    runtimeListOptions: () => ({ queryKey: ["runtimes", "ws-1", "list"] }),
  };
});

vi.mock("@multica/core/pins", () => ({
  pinListOptions: () => ({ queryKey: ["pins", "ws-1", "user-1", "list"] }),
  useCreatePin: () => ({
    isPending: false,
    mutate: mocks.createPin,
  }),
  useDeletePin: () => ({
    isPending: false,
    mutate: mocks.deletePin,
  }),
}));

vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: ({ queryKey }: { queryKey: readonly unknown[] }) => {
      if (queryKey[0] === "runtimes") {
        return {
          data: [{
            id: "runtime-1",
            name: "Studio Mac",
            custom_name: null,
            provider: "codex",
            owner_id: "user-1",
            visibility: "private",
            status: "online",
            daemon_id: "daemon-1",
            metadata: { capabilities: ["screen-mirror-v1"] },
          }],
          isPending: false,
          isSuccess: true,
          error: null,
        };
      }
      return { data: mocks.pins.current, isPending: false, error: null };
    },
  };
});

vi.mock("@multica/ui/components/ui/button", () => ({
  Button: ({
    children,
    ...props
  }: ButtonHTMLAttributes<HTMLButtonElement>) => (
    <button type="button" {...props}>{children}</button>
  ),
}));

vi.mock("../../navigation", () => ({
  AppLink: ({ children, href }: { children: ReactNode; href: string }) => (
    <a href={href}>{children}</a>
  ),
}));

vi.mock("../../layout/breadcrumb-header", () => ({
  BreadcrumbHeader: ({ actions }: { actions: ReactNode }) => (
    <header>{actions}</header>
  ),
}));

vi.mock("./mirror/use-runtime-mirror-session", () => ({
  useRuntimeMirrorSession: () => mocks.session.current,
}));

describe("RuntimeMirrorPage live frame", () => {
  it("renders the complete frame object URL instead of the binary frame state", () => {
    mocks.session.current = {
      state: "streaming",
      failureReason: null,
      imageUrl: "blob:http://localhost/runtime-frame",
      turnConfigured: true,
    };

    renderWithI18n(<RuntimeMirrorPage runtimeId="runtime-1" />);

    const image = screen.getByAltText("Live screen from Studio Mac");
    expect(image).toHaveAttribute("src", "blob:http://localhost/runtime-frame");
  });
});

describe("RuntimeMirrorPage pin action", () => {
  beforeEach(() => {
    mocks.session.current = {
      state: "idle",
      failureReason: null,
      imageUrl: null,
      turnConfigured: true,
    };
    mocks.createPin.mockReset();
    mocks.deletePin.mockReset();
    mocks.pins.current = [];
  });

  it("pins the runtime mirror", () => {
    renderWithI18n(<RuntimeMirrorPage runtimeId="runtime-1" />);

    fireEvent.click(screen.getByRole("button", { name: "Pin to sidebar" }));

    expect(mocks.createPin).toHaveBeenCalledWith({
      item_type: "runtime_mirror",
      item_id: "runtime-1",
    });
  });

  it("unpins the runtime mirror when it is already pinned", () => {
    mocks.pins.current = [{ item_type: "runtime_mirror", item_id: "runtime-1" }];
    renderWithI18n(<RuntimeMirrorPage runtimeId="runtime-1" />);

    fireEvent.click(screen.getByRole("button", { name: "Unpin from sidebar" }));

    expect(mocks.deletePin).toHaveBeenCalledWith({
      itemType: "runtime_mirror",
      itemId: "runtime-1",
    });
  });
});
