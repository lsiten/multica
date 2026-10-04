import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { RESOURCES } from "@multica/views/locales";
import { AppSettingsTab } from "./app-settings-tab";

const save = vi.fn();
let savedConfig = { schemaVersion: 1 as const, apiUrl: "https://api.example.com", wsUrl: "wss://socket.example.com/custom", appUrl: "https://web.example.com", appName: "Multica" };
beforeEach(() => {
  savedConfig = { schemaVersion: 1, apiUrl: "https://api.example.com", wsUrl: "wss://socket.example.com/custom", appUrl: "https://web.example.com", appName: "Multica" };
  save.mockReset().mockImplementation(async (config) => {
    savedConfig = { ...config, ...(config.iconPath ? { iconPath: "/managed/icon.png" } : {}) };
    return savedConfig;
  });
  Object.defineProperty(window, "desktopAPI", { configurable: true, value: {
    runtimeConfig: { ok: true, config: savedConfig },
    getSavedRuntimeConfig: () => ({ ok: true, config: savedConfig }),
    appInfo: { os: "macos" },
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
  expect(await screen.findByText("/managed/icon.png")).toBeInTheDocument();
  expect(screen.queryByText("立即重启")).not.toBeInTheDocument();
});

it("derives endpoints for a new backend", async () => {
  fireEvent.change(screen.getByLabelText("后端 API 地址"), { target: { value: "https://api.new.example" } });
  fireEvent.click(screen.getByText("保存"));
  await waitFor(() => expect(save).toHaveBeenCalledWith(expect.objectContaining({ apiUrl: "https://api.new.example", wsUrl: "wss://api.new.example/ws", appUrl: "https://new.example" })));
  expect(await screen.findByText("立即重启")).toBeInTheDocument();
});

it("reads saved settings when reopening without changing the running endpoints", async () => {
  fireEvent.change(screen.getByLabelText("后端 API 地址"), { target: { value: "https://api.new.example" } });
  fireEvent.click(screen.getByText("保存"));
  await screen.findByText("已保存。");
  cleanup();
  render(<I18nProvider locale="zh-Hans" resources={RESOURCES}><AppSettingsTab /></I18nProvider>);
  expect(screen.getByLabelText("后端 API 地址")).toHaveValue("https://api.new.example");
  expect(window.desktopAPI.runtimeConfig).toMatchObject({ config: { apiUrl: "https://api.example.com" } });
});

it("explains macOS system names and immediate Dock icon updates", () => {
  expect(screen.getByText("macOS 的 Dock 和菜单栏名称由安装包决定，需要重新打包。")).toBeInTheDocument();
  expect(screen.getByText("保存后立即更新运行中的 Dock 图标。")).toBeInTheDocument();
});

it("shows save failures without offering restart", async () => {
  save.mockRejectedValue(new Error("disk full"));
  fireEvent.click(screen.getByText("保存"));
  expect(await screen.findByRole("alert")).toHaveTextContent("disk full");
  expect(screen.queryByText("立即重启")).not.toBeInTheDocument();
});
