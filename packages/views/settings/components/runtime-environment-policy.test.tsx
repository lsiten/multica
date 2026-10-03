import { beforeEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { EnvironmentCommand, EnvironmentPolicyStatus } from "@multica/core/types/environment-operations";
import { renderWithI18n } from "../../test/i18n";
import { RuntimeEnvironmentPolicy } from "./runtime-environment-policy";

const execute = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({ api: { executeRuntimeEnvironment: execute } }));
const status: EnvironmentPolicyStatus = { workspace_id: "ws", runtime_id: "runtime", policy: { enabled: true, archive_after_hours: 24, cache_after_hours: 12, pressure_cache_after_hours: 1, max_idle_environments: 100, max_directory_bytes: 20 * 1024 ** 3, minimum_free_bytes: 5 * 1024 ** 3 }, effective_enabled: true, scan_interval_seconds: 300, free_bytes: 4 * 1024 ** 3, last_scan_at: "2026-10-04T00:00:00Z", idle_environments: 105, directory_bytes: 21 * 1024 ** 3, under_pressure: true };
beforeEach(() => {
  execute.mockReset();
  execute.mockImplementation(async (_workspace: string, _runtime: string, command: EnvironmentCommand) => command.action === "policy_update" ? { ...status, policy: command.policy } : status);
});
function mount(disabled = false) {
  return renderWithI18n(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><RuntimeEnvironmentPolicy workspaceId="ws" runtimeId="runtime" userId="user" disabled={disabled} /></QueryClientProvider>, { locale: "zh-Hans" });
}
it("keeps policy collapsed and saves scoped thresholds in bytes", async () => {
  const user = userEvent.setup(); mount();
  await screen.findByRole("spinbutton", { name: "归档保留期限（小时）" });
  expect(screen.getByText("自动回收策略").closest("details")).not.toHaveAttribute("open");
  await user.click(screen.getByText("自动回收策略"));
  const input = screen.getByRole("spinbutton", { name: "目录大小水位（GiB）" });
  await user.clear(input); await user.type(input, "30");
  await user.click(screen.getByRole("checkbox", { name: "启用自动回收" }));
  await user.click(screen.getByRole("button", { name: "保存策略" }));
  await waitFor(() => expect(execute).toHaveBeenCalledWith("ws", "runtime", { action: "policy_update", policy: expect.objectContaining({ enabled: false, max_directory_bytes: 30 * 1024 ** 3, minimum_free_bytes: 5 * 1024 ** 3, archive_after_hours: 24 }) }));
  expect(await screen.findByRole("status")).toHaveTextContent("策略已保存");
});
it("disables configuration during a conflicting operation", async () => {
  mount(true);
  expect(await screen.findByRole("spinbutton", { name: "归档保留期限（小时）" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "保存策略" })).toBeDisabled();
});
