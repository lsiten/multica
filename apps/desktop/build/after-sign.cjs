"use strict";

// electron-builder afterSign hook (macOS only).
//
// Desktop owns the bundled helper's screen-recording permission. For ad-hoc
// fork builds, keep the containing app's designated requirement stable across
// rebuilds instead of electron-builder's default per-build cdhash requirement.
//
// Certificate-signed builds stay untouched, preserving their signer-bound
// requirements and the exact code tree covered by notarization.

const { execFileSync, spawnSync } = require("node:child_process");
const { existsSync, readdirSync } = require("node:fs");
const { join } = require("node:path");

const CODESIGN = "/usr/bin/codesign";

function log(message) {
  console.log(`[after-sign] ${message}`);
}

function run(args) {
  execFileSync(CODESIGN, args, { stdio: "pipe" });
}

function signatureInfo(appPath) {
  const result = spawnSync(CODESIGN, ["-dv", "--verbose=2", appPath], {
    encoding: "utf8",
  });
  if (result.error || result.status !== 0) {
    throw new Error(`Cannot inspect signature for ${appPath}: ${result.error?.message ?? result.stderr}`);
  }
  return result.stdout + result.stderr;
}

async function pinDesktopIdentity(outerAppPath) {
  const signature = signatureInfo(outerAppPath);
  if (!/^Signature=adhoc\r?$/m.test(signature)) {
    if (/^Authority=.+$/m.test(signature)) {
      log(`certificate signature on ${outerAppPath}, leaving signed code untouched`);
      return;
    }
    throw new Error(`Unrecognized signature for ${outerAppPath}`);
  }

  const identifier = execFileSync("/usr/libexec/PlistBuddy", [
    "-c", "Print :CFBundleIdentifier", join(outerAppPath, "Contents", "Info.plist"),
  ], { encoding: "utf8" }).trim();
  if (!/^[A-Za-z0-9][A-Za-z0-9.-]*$/.test(identifier)) {
    throw new Error(`Invalid bundle identifier for ${outerAppPath}`);
  }

  const entitlements = join(__dirname, "entitlements.mac.plist");
  run([
    "--force",
    "--sign",
    "-",
    "--identifier",
    identifier,
    "-r",
    `=designated => identifier "${identifier}"`,
    "--options",
    "runtime",
    "--entitlements",
    entitlements,
    outerAppPath,
  ]);

  execFileSync(CODESIGN, ["--verify", "--deep", "--strict", outerAppPath], {
    stdio: "pipe",
  });
  log(`pinned stable ${identifier} identity on ${outerAppPath}`);
}

exports.default = async function afterSign(context) {
  if (!context || context.electronPlatformName !== "darwin") return;
  const appOutDir = context.appOutDir;
  if (!appOutDir || !existsSync(appOutDir)) return;

  const outerApps = readdirSync(appOutDir).filter((name) => name.endsWith(".app"));
  for (const name of outerApps) {
    await pinDesktopIdentity(join(appOutDir, name));
  }
};
