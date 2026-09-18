import {
  BrowserWindow,
  screen,
  type IpcMainEvent,
  type IpcMainInvokeEvent,
} from "electron";
import { createRendererWebPreferences } from "./renderer-web-preferences";
import {
  isTrustedRendererURL,
  installNavigationGuard,
} from "./navigation-guard";
import {
  RUNTIME_MIRROR_ARGUMENT,
  parseRuntimeMirrorWindowRequest,
  runtimeMirrorWindowKey,
  type RuntimeMirrorWindowContext,
  type RuntimeMirrorWindowRequest,
} from "../shared/runtime-mirror-window";

export const MIRROR_WINDOW_GEOMETRY = {
  width: 360,
  minWidth: 240,
  maxWidth: 720,
  toolbarHeight: 112,
  inset: 16,
} as const;
export function mirrorWindowBounds(
  workArea: Electron.Rectangle,
  requestedWidth: number = MIRROR_WINDOW_GEOMETRY.width,
) {
  const width = Math.min(
    workArea.width,
    Math.max(
      MIRROR_WINDOW_GEOMETRY.minWidth,
      Math.min(MIRROR_WINDOW_GEOMETRY.maxWidth, requestedWidth),
    ),
  );
  const height =
    Math.round((width * 9) / 16) + MIRROR_WINDOW_GEOMETRY.toolbarHeight;
  return {
    x: workArea.x + workArea.width - width - MIRROR_WINDOW_GEOMETRY.inset,
    y: workArea.y + MIRROR_WINDOW_GEOMETRY.inset,
    width,
    height,
  };
}

type WindowEvent = IpcMainEvent | IpcMainInvokeEvent;
export class RuntimeMirrorWindowManager {
  private readonly windows = new Map<
    string,
    { window: BrowserWindow; context: RuntimeMirrorWindowContext }
  >();
  constructor(
    private readonly host: {
      readonly mainWindow: () => BrowserWindow | null;
      readonly accountId: () => string | null;
      readonly generation: () => number;
      readonly backendIdentity: () => string | null;
      readonly rendererURL: () => string;
      readonly preloadPath: string;
      readonly systemLocale?: () => string;
      readonly register: (window: BrowserWindow) => void;
      readonly unregister: (window: BrowserWindow) => void;
      readonly mediaSourceChanged?: (sources: readonly string[]) => void;
    },
  ) {}

  private trusted(event: WindowEvent, window: BrowserWindow): boolean {
    return (
      event.sender === window.webContents &&
      event.senderFrame === window.webContents.mainFrame &&
      isTrustedRendererURL(event.senderFrame.url, this.host.rendererURL())
    );
  }

  owns(window: BrowserWindow): boolean {
    return [...this.windows.values()].some((entry) => entry.window === window);
  }

  contextFor(event: WindowEvent): RuntimeMirrorWindowContext | null {
    const entry = [...this.windows.values()].find((entry) =>
      this.trusted(event, entry.window),
    );
    return entry &&
      entry.context.generation === this.host.generation() &&
      entry.context.scope.accountId === this.host.accountId() &&
      entry.context.scope.backendIdentity === this.host.backendIdentity()
      ? entry.context
      : null;
  }

  localContext(event: WindowEvent, value: unknown): RuntimeMirrorWindowContext | null {
    const floating = this.contextFor(event);
    const request = parseRuntimeMirrorWindowRequest({ scope: value, title: "" });
    if (!request) return null;
    if (floating) return runtimeMirrorWindowKey(floating.scope) === runtimeMirrorWindowKey(request.scope) ? floating : null;
    const main = this.host.mainWindow();
    if (!main || !this.trusted(event, main) || request.scope.accountId !== this.host.accountId() || request.scope.backendIdentity !== this.host.backendIdentity()) return null;
    return { ...request, kind: "runtime-mirror", generation: this.host.generation() };
  }

  async open(event: WindowEvent, value: unknown): Promise<boolean> {
    const main = this.host.mainWindow();
    const request = parseRuntimeMirrorWindowRequest(value);
    if (
      !main ||
      !this.trusted(event, main) ||
      !request ||
      request.scope.accountId !== this.host.accountId() ||
      request.scope.backendIdentity !== this.host.backendIdentity()
    )
      return false;
    return this.create(request, true) !== null;
  }

  /** Automatic callers supply already-authenticated scope and never activate the window. */
  create(
    request: RuntimeMirrorWindowRequest,
    userInitiated = false,
  ): BrowserWindow | null {
    if (
      request.scope.accountId !== this.host.accountId() ||
      request.scope.backendIdentity !== this.host.backendIdentity()
    )
      return null;
    const key = runtimeMirrorWindowKey(request.scope);
    const existing = this.windows.get(key);
    if (existing && !existing.window.isDestroyed()) {
      if (userInitiated) {
        existing.window.show();
        existing.window.focus();
      }
      return existing.window;
    }
    if (this.windows.size >= 32) return null;
    const main = this.host.mainWindow();
    const display = main
      ? screen.getDisplayMatching(main.getBounds())
      : screen.getDisplayNearestPoint(screen.getCursorScreenPoint());
    const bounds = mirrorWindowBounds(display.workArea);
    const context: RuntimeMirrorWindowContext = {
      ...request,
      kind: "runtime-mirror",
      generation: this.host.generation(),
    };
    const window = new BrowserWindow({
      ...bounds,
      minWidth: MIRROR_WINDOW_GEOMETRY.minWidth,
      maxWidth: MIRROR_WINDOW_GEOMETRY.maxWidth,
      minHeight: 247,
      show: false,
      frame: false,
      resizable: true,
      alwaysOnTop: true,
      skipTaskbar: true,
      title: request.title,
      webPreferences: {
        ...createRendererWebPreferences(
          this.host.preloadPath,
          this.host.systemLocale?.() ?? "en",
          [
            RUNTIME_MIRROR_ARGUMENT +
              encodeURIComponent(JSON.stringify(context)),
          ],
        ),
        plugins: false,
      },
    });
    this.windows.set(key, { window, context });
    this.host.register(window);
    window.setAspectRatio(16 / 9, {
      width: 0,
      height: MIRROR_WINDOW_GEOMETRY.toolbarHeight,
    });
    window.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    installNavigationGuard(window, this.host.rendererURL());
    window.on("ready-to-show", () => {
      if (context.generation !== this.host.generation()) window.close();
      else if (userInitiated) window.show();
      else window.showInactive();
      this.publishExclusions();
    });
    window.on("closed", () => {
      this.windows.delete(key);
      this.host.unregister(window);
      this.publishExclusions();
    });
    void window.loadURL(this.host.rendererURL());
    this.publishExclusions();
    return window;
  }

  resize(event: WindowEvent, width: unknown): boolean {
    if (
      !this.contextFor(event) ||
      typeof width !== "number" ||
      !Number.isFinite(width)
    )
      return false;
    const window = BrowserWindow.fromWebContents(event.sender);
    if (!window) return false;
    const current = window.getBounds();
    const next = mirrorWindowBounds(
      screen.getDisplayMatching(current).workArea,
      width,
    );
    const work = screen.getDisplayMatching(current).workArea;
    window.setBounds({
      x: Math.min(current.x, work.x + work.width - next.width),
      y: Math.min(current.y, work.y + work.height - next.height),
      width: next.width,
      height: next.height,
    });
    return true;
  }

  move(event: WindowEvent, delta: unknown): boolean {
    if (
      !this.contextFor(event) ||
      !delta ||
      typeof delta !== "object" ||
      !("x" in delta) ||
      !("y" in delta) ||
      typeof delta.x !== "number" ||
      typeof delta.y !== "number" ||
      !Number.isFinite(delta.x) ||
      !Number.isFinite(delta.y)
    )
      return false;
    const window = BrowserWindow.fromWebContents(event.sender);
    if (!window) return false;
    const bounds = window.getBounds();
    const work = screen.getDisplayMatching(bounds).workArea;
    window.setPosition(
      Math.round(
        Math.min(
          work.x + work.width - bounds.width,
          Math.max(work.x, bounds.x + delta.x),
        ),
      ),
      Math.round(
        Math.min(
          work.y + work.height - bounds.height,
          Math.max(work.y, bounds.y + delta.y),
        ),
      ),
    );
    return true;
  }

  close(event: WindowEvent): boolean {
    if (!this.contextFor(event)) return false;
    BrowserWindow.fromWebContents(event.sender)?.close();
    return true;
  }

  mediaSourceId(event: WindowEvent): string | null {
    if (!this.contextFor(event)) return null;
    return (
      BrowserWindow.fromWebContents(event.sender)?.getMediaSourceId() ?? null
    );
  }

  closeAll(): void {
    for (const { window } of this.windows.values())
      if (!window.isDestroyed()) window.close();
  }

  private publishExclusions(): void {
    this.host.mediaSourceChanged?.(
      [...this.windows.values()]
        .filter(({ window }) => !window.isDestroyed())
        .map(({ window }) => window.getMediaSourceId()),
    );
  }
}
