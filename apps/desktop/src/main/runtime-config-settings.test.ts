// @vitest-environment node
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { IpcMainEvent, IpcMainInvokeEvent, NativeImage } from "electron";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { DEFAULT_RUNTIME_CONFIG } from "../shared/runtime-config";

const mocks = vi.hoisted(() => ({
  home: "",
  handlers: new Map<string, (event: IpcMainInvokeEvent, input?: unknown) => Promise<unknown>>(),
  listeners: new Map<string, (event: IpcMainEvent) => void>(),
  setDockIcon: vi.fn(),
  setWindowIcon: vi.fn(),
  createFromPath: vi.fn(),
  owner: {},
}));

vi.mock("electron", () => ({
  app: { getPath: () => mocks.home, dock: { setIcon: mocks.setDockIcon } },
  BrowserWindow: {
    fromWebContents: () => mocks.owner,
    getAllWindows: () => [{ setIcon: mocks.setWindowIcon }],
  },
  dialog: {},
  ipcMain: {
    handle: (channel: string, handler: (event: IpcMainInvokeEvent, input?: unknown) => Promise<unknown>) => mocks.handlers.set(channel, handler),
    on: (channel: string, listener: (event: IpcMainEvent) => void) => mocks.listeners.set(channel, listener),
  },
  nativeImage: { createFromPath: mocks.createFromPath },
}));

import { setupRuntimeConfigSettings } from "./runtime-config-settings";

let directory: string;
const event = { sender: {} } as IpcMainInvokeEvent;
const onSaved = vi.fn();
const icon = { isEmpty: () => false } as NativeImage;

beforeEach(async () => {
  vi.clearAllMocks();
  directory = await mkdtemp(join(tmpdir(), "multica-icon-settings-"));
  mocks.home = directory;
  mocks.createFromPath.mockReturnValue(icon);
  setupRuntimeConfigSettings({ ok: true, config: DEFAULT_RUNTIME_CONFIG }, "/bundled/icon.png", onSaved);
});

afterEach(async () => {
  await rm(directory, { recursive: true, force: true });
});

it("copies the selected icon and applies it after saving, without a restart", async () => {
  const source = join(directory, "selected.png");
  await writeFile(source, "selected icon bytes");
  const save = mocks.handlers.get("runtime-config:save")!;
  const stored = await save(event, { ...DEFAULT_RUNTIME_CONFIG, iconPath: source });
  const persisted = JSON.parse(await readFile(join(directory, ".multica/desktop.json"), "utf8"));
  expect(stored).toEqual(persisted);
  expect(persisted.iconPath).not.toBe(source);
  expect(await readFile(persisted.iconPath, "utf8")).toBe("selected icon bytes");
  expect(mocks.createFromPath).toHaveBeenLastCalledWith(persisted.iconPath);
  expect(process.platform === "darwin" ? mocks.setDockIcon : mocks.setWindowIcon).toHaveBeenCalledWith(icon);
  expect(onSaved).toHaveBeenCalledWith(stored);
  const readEvent = {} as IpcMainEvent;
  mocks.listeners.get("runtime-config:settings-get")!(readEvent);
  expect(readEvent.returnValue).toEqual({ ok: true, config: stored });
});

it("restores the bundled icon when the selected icon is cleared", async () => {
  await mocks.handlers.get("runtime-config:save")!(event, DEFAULT_RUNTIME_CONFIG);
  expect(mocks.createFromPath).toHaveBeenLastCalledWith("/bundled/icon.png");
  expect(process.platform === "darwin" ? mocks.setDockIcon : mocks.setWindowIcon).toHaveBeenCalledWith(icon);
});

it("keeps the running icon and saved settings when persistence fails", async () => {
  await writeFile(join(directory, ".multica"), "not a directory");
  await expect(mocks.handlers.get("runtime-config:save")!(event, DEFAULT_RUNTIME_CONFIG)).rejects.toThrow();
  expect(mocks.setDockIcon).not.toHaveBeenCalled();
  expect(mocks.setWindowIcon).not.toHaveBeenCalled();
  expect(onSaved).not.toHaveBeenCalled();
  const readEvent = {} as IpcMainEvent;
  mocks.listeners.get("runtime-config:settings-get")!(readEvent);
  expect(readEvent.returnValue).toEqual({ ok: true, config: DEFAULT_RUNTIME_CONFIG });
});

it("rejects invalid images before saving or updating the Dock", async () => {
  mocks.createFromPath.mockReturnValue({ isEmpty: () => true });
  await expect(mocks.handlers.get("runtime-config:save")!(event, { ...DEFAULT_RUNTIME_CONFIG, iconPath: "/invalid.png" })).rejects.toThrow("Invalid application icon");
  expect(mocks.setDockIcon).not.toHaveBeenCalled();
  expect(onSaved).not.toHaveBeenCalled();
});
