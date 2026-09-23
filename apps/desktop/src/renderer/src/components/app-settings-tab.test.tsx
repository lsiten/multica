import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { RESOURCES } from "@multica/views/locales";
import { AppSettingsTab } from "./app-settings-tab";

const save = vi.fn();
beforeEach(() => {
  save.mockReset().mockResolvedValue({});
  Object.defineProperty(window, "desktopAPI", { configurable: true, value: {
    runtimeConfig: { ok: true, config: { schemaVersion: 1, apiUrl: "https://api.example.com", wsUrl: "wss://socket.example.com/custom", appUrl: "https://web.example.com", appName: "Multica" } },
    saveRuntimeConfig: save,
    pickAppIcon: vi.fn().mockResolvedValue("/tmp/custom.png"),
    restartApp: vi.fn(),
  } });
  render(<I18nProvider locale="zh-Hans" resources={RESOURCES}><AppSettingsTab /></I18nProvider>);
});

it("preserves explicit endpoints when changing branding", async () => {
  fireEvent.change(screen.getByLabelText("应用名称"), { target: { value: "My App" } });
  fireEvent.click(screen.getByText("选择图片"));
  await screen.findByText("/tmp/custom.png");
  fireEvent.click(screen.getByText("保存"));
  await waitFor(() => expect(save).toHaveBeenCalledWith(expect.objectContaining({ appName: "My App", iconPath: "/tmp/custom.png", wsUrl: "wss://socket.example.com/custom", appUrl: "https://web.example.com" })));
  expect(await screen.findByText("立即重启")).toBeInTheDocument();
});

it("derives endpoints for a new backend", async () => {
  fireEvent.change(screen.getByLabelText("后端 API 地址"), { target: { value: "https://api.new.example" } });
  fireEvent.click(screen.getByText("保存"));
  await waitFor(() => expect(save).toHaveBeenCalledWith(expect.objectContaining({ apiUrl: "https://api.new.example", wsUrl: "wss://api.new.example/ws", appUrl: "https://new.example" })));
});

it("shows save failures without offering restart", async () => {
  save.mockRejectedValue(new Error("disk full"));
  fireEvent.click(screen.getByText("保存"));
  expect(await screen.findByRole("alert")).toHaveTextContent("disk full");
  expect(screen.queryByText("立即重启")).not.toBeInTheDocument();
});
