// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ReactNode } from "react";
import { RESOURCES } from "../../../test/i18n";
import { LegacyRuntimeMirrorPage } from "./legacy-mirror-page";

vi.mock("./use-runtime-mirror-session", () => ({
  useRuntimeMirrorSession: () => ({
    state: "streaming",
    imageUrl: "data:image/png;base64,AAA",
    turnConfigured: true,
    failureReason: null,
  }),
}));
vi.mock("@multica/core/runtimes", () => ({
  isRuntimeUsableForUser: () => true,
  runtimeDisplayLabel: () => "Fixture runtime",
  runtimeListOptions: () => ({ queryKey: ["runtimes"], queryFn: async () => [] }),
  runtimeMirrorAvailability: () => ({ kind: "supported", enabled: true }),
}));
vi.mock("@multica/core/pins", () => ({
  pinListOptions: () => ({ queryKey: ["pins"], queryFn: async () => [] }),
  useCreatePin: () => ({ mutate: () => {}, isPending: false }),
  useDeletePin: () => ({ mutate: () => {}, isPending: false }),
}));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "owner" } }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    runtimes: () => "/runtimes",
    runtimeDetail: () => "/runtimes/runtime",
  }),
}));
vi.mock("../../../navigation", () => ({
  AppLink: (props: { href: string; children: ReactNode }) => (
    <a href={props.href}>{props.children}</a>
  ),
}));
vi.mock("../../../layout/breadcrumb-header", () => ({
  BreadcrumbHeader: () => null,
}));

function renderPage(connectionState: "streaming" | "negotiating") {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={queryClient}>
        {children}
      </QueryClientProvider>
    </I18nProvider>
  );
  render(
    <LegacyRuntimeMirrorPage
      runtimeId="runtime"
      connectionState={connectionState}
      scope={{
        backendIdentity: "https://fixture.invalid",
        accountId: "owner",
        workspaceId: "workspace",
        runtimeId: "runtime",
      }}
    />,
    { wrapper },
  );
}

describe("LegacyRuntimeMirrorPage main-display guidance", () => {
  it("guides multi-display users to v2 source selection while streaming the main display", () => {
    renderPage("streaming");
    expect(
      screen.getByText(
        "This view shows only the main display. To see other displays, enable v2 video mirroring with source selection.",
      ),
    ).toBeInTheDocument();
  });

  it("does not show the main-display guidance before the stream starts", () => {
    renderPage("negotiating");
    expect(
      screen.queryByText(
        "This view shows only the main display. To see other displays, enable v2 video mirroring with source selection.",
      ),
    ).not.toBeInTheDocument();
  });
});
