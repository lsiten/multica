// @vitest-environment jsdom

import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";
import type { MirrorNetworkSettings } from "@multica/core/types";

const mockMutate = vi.hoisted(() => vi.fn());

const state = vi.hoisted(() => ({
  settings: null as MirrorNetworkSettings | null,
  isLoading: false,
}));

function settings(over: Partial<MirrorNetworkSettings> = {}): MirrorNetworkSettings {
  return {
    source: "builtin",
    locked: false,
    can_manage: true,
    turn_configured: true,
    mode: "builtin",
    cloudflare: {
      enabled: false,
      available: false,
      healthy: false,
      has_api_token: false,
    },
    builtin: {
      enabled: true,
      available: true,
      host: "turn.example.com",
      port: 3478,
      transports: ["udp", "tcp"],
      credential_ttl_seconds: 3600,
    },
    custom: [],
    ...over,
  };
}

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: state.settings, isLoading: state.isLoading }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  workspaceMirrorNetworkOptions: () => ({
    queryKey: ["workspaces", "workspace-1", "mirror-network"],
  }),
}));

vi.mock("@multica/core/workspace/mutations", () => ({
  useUpdateWorkspaceMirrorNetwork: () => ({
    mutateAsync: mockMutate,
    isPending: false,
  }),
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "workspace-1", name: "Acme", slug: "acme" }),
}));

const toastError = vi.fn();
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: (...args: unknown[]) => toastError(...args) },
}));

import { MirrorNetworkTab } from "./mirror-network-tab";

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
      {children}
    </I18nProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  state.isLoading = false;
  state.settings = settings();
  mockMutate.mockResolvedValue(settings());
});

describe("MirrorNetworkTab", () => {
  it("renders the built-in relay status and host", () => {
    render(<MirrorNetworkTab />, { wrapper: Wrapper });
    expect(screen.getByText("TURN ready")).toBeInTheDocument();
    expect(screen.getByText(/turn\.example\.com:3478/)).toBeInTheDocument();
  });

  it("warns when no TURN is configured", () => {
    state.settings = settings({
      source: "builtin_unavailable",
      turn_configured: false,
      mode: "builtin",
      builtin: {
        enabled: true,
        available: false,
      },
    });
    render(<MirrorNetworkTab />, { wrapper: Wrapper });
    expect(screen.getByText("No TURN server is currently available")).toBeInTheDocument();
  });

  it("saves the built-in mode", async () => {
    render(<MirrorNetworkTab />, { wrapper: Wrapper });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(mockMutate).toHaveBeenCalledWith({ mode: "builtin" }));
  });

  it("sends custom servers with at least one TURN url", async () => {
    render(<MirrorNetworkTab />, { wrapper: Wrapper });
    fireEvent.click(screen.getByRole("radio", { name: "Custom servers" }));
    const urlsBox = screen.getByPlaceholderText(
      "turn:turn.example.com:3478?transport=udp",
    );
    fireEvent.change(urlsBox, {
      target: {
        value: "stun:stun.example.com:3478\nturn:turn.example.com:3478?transport=udp",
      },
    });
    fireEvent.change(screen.getByLabelText("Username (optional)"), {
      target: { value: "viewer" },
    });
    fireEvent.change(screen.getByLabelText("Credential (optional)"), {
      target: { value: "secret" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(mockMutate).toHaveBeenCalledWith({
        mode: "custom",
        servers: [
          {
            urls: [
              "stun:stun.example.com:3478",
              "turn:turn.example.com:3478?transport=udp",
            ],
            username: "viewer",
            credential: "secret",
          },
        ],
      }),
    );
  });

  it("rejects custom mode without a TURN url", () => {
    render(<MirrorNetworkTab />, { wrapper: Wrapper });
    fireEvent.click(screen.getByRole("radio", { name: "Custom servers" }));
    fireEvent.change(
      screen.getByPlaceholderText("turn:turn.example.com:3478?transport=udp"),
      { target: { value: "stun:stun.example.com:3478" } },
    );
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(mockMutate).not.toHaveBeenCalled();
    expect(toastError).toHaveBeenCalledWith(
      "Custom mode requires at least one server with a turn: or turns: URL",
    );
  });

  it("hints that a stored credential is kept when left empty", () => {
    state.settings = settings({
      mode: "custom",
      source: "custom",
      custom: [
        {
          urls: ["turn:turn.example.com:3478?transport=udp"],
          username: "viewer",
          has_credential: true,
        },
      ],
    });
    render(<MirrorNetworkTab />, { wrapper: Wrapper });
    expect(
      screen.getByText(
        "The credential is stored encrypted. Saving with this field empty keeps the existing credential; typing a new value overwrites it.",
      ),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    // The credential field is omitted so the server preserves the secret.
    expect(mockMutate).toHaveBeenCalledWith({
      mode: "custom",
      servers: [
        {
          urls: ["turn:turn.example.com:3478?transport=udp"],
          username: "viewer",
        },
      ],
    });
  });

  it("is fully read-only when deployment-locked", () => {
    state.settings = settings({ locked: true, can_manage: false, source: "env" });
    render(<MirrorNetworkTab />, { wrapper: Wrapper });
    expect(
      screen.getByText("Locked by deployment configuration"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Built-in TURN (recommended)" })).toBeDisabled();
  });

  it("is read-only for a member without manage rights", () => {
    state.settings = settings({ can_manage: false });
    render(<MirrorNetworkTab />, { wrapper: Wrapper });
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  });
});
