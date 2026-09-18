import { app } from "electron";
import { createRequire } from "node:module";
import { runDesktopNativeSmoke } from "./native-smoke";

// Keep smoke isolation ahead of product imports, login-shell recovery and the app lock.
if (process.env.MULTICA_DESKTOP_NATIVE_SMOKE_FILE !== undefined) {
  void runDesktopNativeSmoke(app, process.env.MULTICA_DESKTOP_NATIVE_SMOKE_FILE, { entryPath: __filename })
    .then((code) => app.exit(code), () => app.exit(1));
} else {
  createRequire(__filename)("./normal-startup.js");
}
