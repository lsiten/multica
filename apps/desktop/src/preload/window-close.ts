import { ipcRenderer } from "electron";
import { WINDOW_CLOSE_CHECK, WINDOW_CLOSE_REPLY } from "../shared/window-close";

export function installWindowCloseCheck(): void {
  ipcRenderer.on(WINDOW_CLOSE_CHECK, (_event, id: unknown) => {
    if (typeof id !== "number" || !Number.isSafeInteger(id)) return;
    const allowed = window.dispatchEvent(new Event("beforeunload", { cancelable: true }));
    ipcRenderer.send(WINDOW_CLOSE_REPLY, { id, allowed });
  });
}
