// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { VscreenScope, VscreenSourceDescriptor } from "@multica/core/types";
import { RESOURCES } from "../../../test/i18n";
import { useMirrorControllers } from "./use-mirror-controllers";

const scope: VscreenScope = {
  backendIdentity: "https://fixture.invalid",
  accountId: "owner",
  workspaceId: "workspace",
  runtimeId: "runtime",
};

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: vi.fn(),
}));

vi.mock("@multica/core/runtimes", () => ({
  vscreenKeys: {
    controlState: () => ["control-state"],
  },
  vscreenControlStateOptions: () => ({
    queryKey: ["control-state"],
    queryFn: async () => ({
      workspaceId: scope.workspaceId,
      runtimeId: scope.runtimeId,
      controllers: [
        {
          viewerId: "viewer",
          userId: "unknown-user",
          source: { kind: "physical", sourceId: "display:1" },
        },
      ],
    }),
  }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({
    queryKey: ["members"],
    queryFn: async () => [],
  }),
}));

describe("useMirrorControllers", () => {
  it("uses localized fallback text instead of a raw English member label", async () => {
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

    const { result } = renderHook(
      () =>
        useMirrorControllers({
          scope,
          enabled: true,
          catalog: [
            {
              resource: {
                backendIdentity: scope.backendIdentity,
                workspaceId: scope.workspaceId,
                runtimeId: scope.runtimeId,
                uid: 501,
              },
              source: { kind: "physical", sourceId: "display:1" },
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
              displayId: 1,
            } satisfies VscreenSourceDescriptor,
          ],
        }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.controllers).toHaveLength(1));
    expect(result.current.controllers[0]?.name).toBe("A member");
    expect(result.current.controllers[0]?.sourceLabel).toBe("Display 1");
  });
});
