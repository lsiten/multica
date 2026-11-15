import { contextBridge, ipcRenderer } from "electron";
import {
  RUNTIME_MIRROR_CHANNEL,
  readRuntimeMirrorWindowContext,
} from "../shared/runtime-mirror-window";
import { AUTH_SESSION_STATE_CHANNEL } from "../shared/auth-session";
import { parseRuntimeConfig } from "../shared/runtime-config";

function runtimeConfig(config: unknown) {
  if (
    !config ||
    typeof config !== "object" ||
    !("ok" in config) ||
    config.ok !== true ||
    !("config" in config)
  )
    return null;
  try {
    return parseRuntimeConfig(JSON.stringify(config.config));
  } catch (error) {
    if (error instanceof Error) return null;
    throw error;
  }
}
function createRuntimeMirrorPreloadAPI() {
  return {
    context: readRuntimeMirrorWindowContext(process.argv),
    systemLocale:
      process.argv
        .find((argument) => argument.startsWith("--multica-locale="))
        ?.slice("--multica-locale=".length) ?? "en",
    runtimeConfig: runtimeConfig(ipcRenderer.sendSync("runtime-config:get")),
    reportAuthSession: (accountId: string | null) =>
      ipcRenderer.send(AUTH_SESSION_STATE_CHANNEL, accountId),
    close: (): Promise<boolean> =>
      ipcRenderer.invoke(RUNTIME_MIRROR_CHANNEL, "close"),
    resize: (width: number): Promise<boolean> =>
      ipcRenderer.invoke(RUNTIME_MIRROR_CHANNEL, "resize", width),
    move: (delta: {
      readonly x: number;
      readonly y: number;
    }): Promise<boolean> =>
      ipcRenderer.invoke(RUNTIME_MIRROR_CHANNEL, "move", delta),
    getMediaSourceId: (): Promise<string | null> =>
      ipcRenderer.invoke(RUNTIME_MIRROR_CHANNEL, "media-source"),
  };
}

export type RuntimeMirrorPreloadAPI = ReturnType<
  typeof createRuntimeMirrorPreloadAPI
>;
export function exposeRuntimeMirrorAPI(): void {
  contextBridge.exposeInMainWorld(
    "runtimeMirrorAPI",
    createRuntimeMirrorPreloadAPI(),
  );
}
