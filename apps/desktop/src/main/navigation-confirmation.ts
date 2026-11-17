import { BrowserWindow, dialog, ipcMain } from "electron";
import { NAVIGATION_CONFIRM_CHANNEL, parseNavigationConfirmation } from "../shared/navigation-confirmation";

export function installNavigationConfirmation(): void {
  ipcMain.on(NAVIGATION_CONFIRM_CHANNEL, (event, input: unknown) => {
    const window = BrowserWindow.fromWebContents(event.sender);
    const request = parseNavigationConfirmation(input);
    if (!window || !request || event.senderFrame !== event.sender.mainFrame) {
      event.returnValue = false;
      return;
    }
    const choice = dialog.showMessageBoxSync(window, {
      type: "warning",
      message: request.message,
      buttons: [request.cancelLabel, request.confirmLabel],
      defaultId: 0,
      cancelId: 0,
      noLink: true,
    });
    event.returnValue = choice === 1;
  });
}
