import { session, systemPreferences } from "electron";

export async function requestMicrophoneAccess(): Promise<boolean> {
  switch (systemPreferences.getMediaAccessStatus("microphone")) {
    case "granted": return true;
    case "not-determined": return systemPreferences.askForMediaAccess("microphone");
    case "denied":
    case "restricted":
    case "unknown": return false;
  }
}

export function setupMicrophonePermission(): void {
  if (process.platform !== "darwin") return;
  session.defaultSession.setPermissionRequestHandler((_contents, permission, callback, details) => {
    if (permission !== "media" || ("mediaTypes" in details && details.mediaTypes && !details.mediaTypes.includes("audio"))) {
      callback(true);
      return;
    }
    void requestMicrophoneAccess().then(callback, (error: unknown) => {
      console.error("[voice] microphone permission request failed", error);
      callback(false);
    });
  });
}
