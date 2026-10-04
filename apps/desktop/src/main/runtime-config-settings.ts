import { app, BrowserWindow, dialog, ipcMain, nativeImage } from "electron";
import { saveRuntimeConfig } from "./runtime-config-loader";
import { parseRuntimeConfig, type RuntimeConfig, type RuntimeConfigResult } from "../shared/runtime-config";

export function applyAppIcon(iconPath: string): void {
  const icon = nativeImage.createFromPath(iconPath);
  if (icon.isEmpty()) throw new Error("Invalid application icon");
  if (process.platform === "darwin") {
    app.dock?.setIcon(icon);
  } else {
    for (const window of BrowserWindow.getAllWindows()) window.setIcon(icon);
  }
}

export function setupRuntimeConfigSettings(
  initial: RuntimeConfigResult,
  bundledIconPath: string,
  onSaved: (config: RuntimeConfig) => void,
): void {
  let savedConfig = initial;
  ipcMain.on("runtime-config:settings-get", (event) => {
    event.returnValue = savedConfig;
  });

  ipcMain.handle("runtime-config:pick-icon", async (event) => {
    const owner = BrowserWindow.fromWebContents(event.sender);
    if (!owner) throw new Error("Invalid settings window");
    const result = await dialog.showOpenDialog(owner, {
      properties: ["openFile"],
      filters: [{ name: "Images", extensions: ["png", "jpg", "jpeg", "ico", "icns"] }],
    });
    const path = result.filePaths[0];
    if (result.canceled || !path) return null;
    if (nativeImage.createFromPath(path).isEmpty()) throw new Error("Invalid application icon");
    return path;
  });

  ipcMain.handle("runtime-config:save", async (event, input: unknown) => {
    if (!BrowserWindow.fromWebContents(event.sender)) throw new Error("Invalid settings window");
    const config = parseRuntimeConfig(JSON.stringify(input));
    if (config.iconPath && nativeImage.createFromPath(config.iconPath).isEmpty()) {
      throw new Error("Invalid application icon");
    }
    const stored = await saveRuntimeConfig(config);
    applyAppIcon(stored.iconPath ?? bundledIconPath);
    savedConfig = { ok: true, config: stored };
    onSaved(stored);
    return stored;
  });

  ipcMain.handle("runtime-config:restart", (event) => {
    if (!BrowserWindow.fromWebContents(event.sender)) throw new Error("Invalid settings window");
    app.relaunch();
    app.quit();
  });
}
