// @vitest-environment node
import { beforeEach, expect, it, vi } from "vitest";
import { WINDOW_CLOSE_CHECK } from "../shared/window-close";

interface ReplyEvent {
  readonly sender: { readonly mainFrame: object };
  readonly senderFrame: object;
}
interface QuitEvent { defaultPrevented: boolean }
const mocks = vi.hoisted(() => ({
  reply: undefined as ((event: ReplyEvent, input: unknown) => void) | undefined,
  fromWebContents: vi.fn(),
  quit: vi.fn(),
  closeDeclined: vi.fn(),
  appListeners: new Map<string, (event: QuitEvent) => void>(),
}));
vi.mock("electron", () => ({
  app: { on: (name: string, listener: (event: QuitEvent) => void) => { mocks.appListeners.set(name, listener); }, quit: mocks.quit },
  BrowserWindow: { fromWebContents: mocks.fromWebContents },
  ipcMain: { on: (_channel: string, listener: (event: ReplyEvent, input: unknown) => void) => { mocks.reply = listener; } },
}));
import { installWindowCloseGuard, installWindowCloseIPC } from "./window-close-guard";

beforeEach(() => { vi.clearAllMocks(); mocks.appListeners.clear(); installWindowCloseIPC({ platform: "darwin", onCloseDeclined: mocks.closeDeclined }); });
function fixture() {
  let close: ((event: { preventDefault(): void }) => void) | undefined;
  let navigate: ((details: { isMainFrame: boolean; isSameDocument: boolean }) => void) | undefined;
  const frame = {};
  const window = {
    webContents: {
      isLoadingMainFrame: vi.fn(() => false),
      getOSProcessId: vi.fn(() => 100),
      send: vi.fn(),
      on: (_name: "did-start-navigation", listener: (details: { isMainFrame: boolean; isSameDocument: boolean }) => void) => { navigate = listener; },
    },
    on: (_name: "close", listener: (event: { preventDefault(): void }) => void) => { close = listener; },
    destroy: vi.fn(),
  };
  mocks.fromWebContents.mockReturnValue(window);
  installWindowCloseGuard(window);
  const event = { preventDefault: vi.fn() };
  const reply = (allowed: unknown, id: unknown = window.webContents.send.mock.calls.at(-1)?.[1], senderFrame = frame) => mocks.reply?.({ sender: { mainFrame: frame }, senderFrame }, { id, allowed });
  return { window, event, close: () => close?.(event), navigate: (isMainFrame = true, isSameDocument = false) => navigate?.({ isMainFrame, isSameDocument }), reply };
}
it("keeps the renderer mounted until a matching explicit approval arrives", () => {
  const f = fixture();
  f.close();
  f.close();
  expect(f.event.preventDefault).toHaveBeenCalledTimes(2);
  expect(f.window.webContents.send).toHaveBeenCalledTimes(1);
  expect(f.window.webContents.send).toHaveBeenCalledWith(WINDOW_CLOSE_CHECK, expect.any(Number));
  expect(f.window.destroy).not.toHaveBeenCalled();
  f.reply(false);
  expect(f.window.destroy).not.toHaveBeenCalled();
  expect(mocks.closeDeclined).toHaveBeenCalledOnce();
  f.close();
  expect(f.window.webContents.send).toHaveBeenCalledTimes(2);
  f.reply(true);
  expect(f.window.destroy).toHaveBeenCalledOnce();
});
it("rejects stale, malformed and subframe approvals", () => {
  const f = fixture();
  f.close();
  const id = f.window.webContents.send.mock.calls[0]?.[1];
  f.reply(true, -1);
  f.reply("true", id);
  f.reply(true, id, {});
  expect(f.window.destroy).not.toHaveBeenCalled();
  f.navigate();
  f.reply(true, id);
  expect(f.window.destroy).not.toHaveBeenCalled();
  f.close();
  f.reply(true);
  expect(f.window.destroy).toHaveBeenCalledOnce();
});
it("leaves loading and already-crashed windows closable", () => {
  const f = fixture();
  f.window.webContents.isLoadingMainFrame.mockReturnValue(true);
  f.close();
  f.window.webContents.isLoadingMainFrame.mockReturnValue(false);
  f.window.webContents.getOSProcessId.mockReturnValue(0);
  f.close();
  expect(f.event.preventDefault).not.toHaveBeenCalled();
  expect(f.window.webContents.send).not.toHaveBeenCalled();
});
it("keeps a close decision valid across subframe or same-document navigation", () => {
  const f = fixture();
  f.close();
  f.navigate(false);
  f.navigate(true, true);
  f.reply(true);
  expect(f.window.destroy).toHaveBeenCalledOnce();
});
it("resumes a macOS quit after its asynchronous window decisions finish", async () => {
  const f = fixture();
  mocks.appListeners.get("before-quit")?.({ defaultPrevented: false });
  await Promise.resolve();
  f.close();
  f.reply(true);
  mocks.appListeners.get("window-all-closed")?.({ defaultPrevented: false });
  expect(mocks.quit).toHaveBeenCalledOnce();
});
it("keeps macOS running when the member declines closing a window", async () => {
  const f = fixture();
  mocks.appListeners.get("before-quit")?.({ defaultPrevented: false });
  await Promise.resolve();
  f.close();
  f.reply(false);
  mocks.appListeners.get("window-all-closed")?.({ defaultPrevented: false });
  expect(mocks.quit).not.toHaveBeenCalled();
});
it("respects a quit veto from another lifecycle listener", async () => {
  const event = { defaultPrevented: false };
  mocks.appListeners.get("before-quit")?.(event);
  event.defaultPrevented = true;
  await Promise.resolve();
  mocks.appListeners.get("window-all-closed")?.({ defaultPrevented: false });
  expect(mocks.quit).not.toHaveBeenCalled();
});
it("leaves non-macOS all-windows-closed handling with the existing application lifecycle", async () => {
  installWindowCloseIPC({ platform: "win32", onCloseDeclined: mocks.closeDeclined });
  mocks.appListeners.get("before-quit")?.({ defaultPrevented: false });
  await Promise.resolve();
  mocks.appListeners.get("window-all-closed")?.({ defaultPrevented: false });
  expect(mocks.quit).not.toHaveBeenCalled();
});
