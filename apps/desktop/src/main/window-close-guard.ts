import { app, BrowserWindow, ipcMain } from "electron";
import { parseWindowCloseReply, WINDOW_CLOSE_CHECK, WINDOW_CLOSE_REPLY } from "../shared/window-close";

interface CloseGuardWindow {
  readonly webContents: {
    isLoadingMainFrame(): boolean;
    getOSProcessId(): number;
    send(channel: string, id: number): void;
    on(event: "did-start-navigation", listener: (details: { isMainFrame: boolean; isSameDocument: boolean }) => void): unknown;
  };
  on(event: "close", listener: (event: { preventDefault(): void }) => void): unknown;
  destroy(): void;
}

const pending = new WeakMap<CloseGuardWindow, number>();
let sequence = 0;

export function installWindowCloseIPC(options: { readonly platform: NodeJS.Platform; readonly onCloseDeclined: () => void }): void {
  let quitRequested = false;
  app.on("before-quit", (event) => {
    // Updater and daemon gates may veto later in the same event dispatch.
    queueMicrotask(() => { quitRequested = !event.defaultPrevented; });
  });
  app.on("window-all-closed", () => {
    if (quitRequested && options.platform === "darwin") app.quit();
  });
  ipcMain.on(WINDOW_CLOSE_REPLY, (event, input: unknown) => {
    const window = BrowserWindow.fromWebContents(event.sender);
    const reply = parseWindowCloseReply(input);
    if (!window || !reply || event.senderFrame !== event.sender.mainFrame || pending.get(window) !== reply.id) return;
    pending.delete(window);
    // All renderer unload listeners have already run before this approval.
    if (reply.allowed) window.destroy();
    else {
      quitRequested = false;
      options.onCloseDeclined();
    }
  });
}

export function installWindowCloseGuard(window: CloseGuardWindow): void {
  window.webContents.on("did-start-navigation", (details) => {
    if (details.isMainFrame && !details.isSameDocument) pending.delete(window);
  });
  window.on("close", (event) => {
    if (window.webContents.isLoadingMainFrame() || window.webContents.getOSProcessId() === 0) return;
    event.preventDefault();
    if (pending.has(window)) return;
    const id = ++sequence;
    pending.set(window, id);
    window.webContents.send(WINDOW_CLOSE_CHECK, id);
  });
}
