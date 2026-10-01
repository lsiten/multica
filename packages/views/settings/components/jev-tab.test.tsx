// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../../test/i18n";

const config = vi.hoisted(() => ({
  current: {
    data: {
      workspace_id: "ws-1",
    revision: 3,
    config: {
      source: "agent_context" as const,
      timeout_seconds: 30,
      revision: 3,
      },
    },
    isLoading: false,
    isError: false,
  },
}));
const api = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn() }));
const daemon = vi.hoisted(() => ({
  getJevModels: vi.fn(),
  installJevModel: vi.fn(),
  cancelJevModelInstall: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => config.current,
  useQueryClient: () => ({ setQueryData: vi.fn() }),
  useMutation: (options: { mutationFn: (value: unknown) => Promise<unknown>; onSuccess?: (value: any) => void; onError?: (error: unknown) => void }) => ({
    isPending: false,
    mutate: (value: unknown) => {
      void options.mutationFn(value).then((result) => options.onSuccess?.(result)).catch((error) => options.onError?.(error));
    },
  }),
}));
vi.mock("@multica/core/api", () => ({ api: {
  getWorkspaceJevConfig: api.get,
  updateWorkspaceJevConfig: api.update,
} }));
vi.mock("@multica/core/paths", () => ({ useCurrentWorkspace: () => ({ id: "ws-1", name: "Acme" }) }));
vi.mock("@multica/core/permissions", () => ({ useCurrentMember: () => ({ role: "owner" }) }));

import { JevTab } from "./jev-tab";

const INITIAL = {
  workspace_id: "ws-1",
  revision: 3,
  config: { source: "agent_context" as const, timeout_seconds: 30, revision: 3 },
};

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  config.current = { data: INITIAL, isLoading: false, isError: false };
  api.get.mockResolvedValue(INITIAL);
  api.update.mockResolvedValue({ ...INITIAL, revision: 4, config: { ...INITIAL.config, revision: 4 } });
  daemon.getJevModels.mockResolvedValue({ models: [], status: [] });
  daemon.installJevModel.mockResolvedValue({ accepted: true });
  daemon.cancelJevModelInstall.mockResolvedValue({ cancelled: true });
  Object.defineProperty(window, "daemonAPI", { configurable: true, value: daemon });
});

describe("JevTab", () => {
  it("does not save until a workspace source change is explicit", async () => {
    const user = userEvent.setup();
    renderWithI18n(<JevTab />);
    expect((screen.getByRole("button", { name: "Save Jev configuration" }) as HTMLButtonElement).disabled).toBe(true);
    await user.selectOptions(screen.getAllByRole("combobox", { name: "Model source" })[0]!, "local");
    expect((screen.getByRole("button", { name: "Save Jev configuration" }) as HTMLButtonElement).disabled).toBe(false);
    await user.click(screen.getByRole("button", { name: "Save Jev configuration" }));
    await waitFor(() => expect(api.update).toHaveBeenCalledWith("ws-1", expect.objectContaining({ revision: 3 })));
  });

  it("shows a web/daemon boundary and requires an explicit download click", async () => {
    const user = userEvent.setup();
    renderWithI18n(<JevTab />);
    await user.selectOptions(screen.getAllByRole("combobox", { name: "Model source" })[0]!, "local");
    expect(screen.getByText("The model is downloaded to the host daemon only after confirmation. The desktop package never includes model weights.")).toBeTruthy();
    expect(daemon.installJevModel).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Confirm and download" }));
    await waitFor(() => expect(daemon.installJevModel).toHaveBeenCalledWith("Mapika/decider-2b"));
  });

  it("polls installation state and offers cancellation", async () => {
    const user = userEvent.setup();
    daemon.getJevModels.mockResolvedValue({ status: [{ model_id: "Mapika/decider-2b", state: "downloading", downloaded_bytes: 10, total_bytes: 100 }] });
    renderWithI18n(<JevTab />);
    await user.selectOptions(screen.getAllByRole("combobox", { name: "Model source" })[0]!, "local");
    await waitFor(() => expect(screen.getByRole("button", { name: "Cancel download" })).toBeTruthy());
    await user.click(screen.getByRole("button", { name: "Cancel download" }));
    await waitFor(() => expect(daemon.cancelJevModelInstall).toHaveBeenCalledWith("Mapika/decider-2b"));
  });

  it("reports API failures instead of staying on a loading screen", () => {
    config.current = { data: INITIAL, isLoading: false, isError: true };
    renderWithI18n(<JevTab />);
    expect(screen.getByRole("alert").textContent).toContain("Failed to load Jev configuration.");
  });
});
