"use strict";

// electron-builder afterSign hook (macOS only).
//
// Fork builds are ad-hoc signed. electron-builder gives nested Mach-O files a
// per-build cdhash identity, which made the real daemon in
// app.asar.unpacked/resources/bin unrecognizable to TCC after every update.
// The product daemon ships as MulticaDaemon.app; this hook re-pins both the
// daemon and containing Multica.app to identifier-based designated
// requirements after electron-builder has signed the tree.
//
// Certificate-signed builds stay untouched, preserving their signer-bound
// requirements and the exact code tree covered by notarization.

const { execFileSync, spawnSync } = require("node:child_process");
const { existsSync, readdirSync } = require("node:fs");
const { join } = require("node:path");

const CODESIGN = "/usr/bin/codesign";
const PLIST_BUDDY = "/usr/libexec/PlistBuddy";
const DAEMON_APP_NAME = "MulticaDaemon.app";
const DAEMON_BINARY_NAME = "multica";
const DAEMON_IDENTIFIER = "ai.multica.daemon";
const DAEMON_REQUIREMENT = `=designated => identifier "${DAEMON_IDENTIFIER}"`;

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
  const output = `${result.stdout ?? ""}${result.stderr ?? ""}`;
  if (
    result.status === 1 &&
    /: code object is not signed at all\r?\n?$/.test(output)
  ) {
    return null;
  }
  if (result.error || result.status !== 0) {
    throw new Error(
      `Cannot inspect signature for ${appPath}: ${result.error?.message ?? output}`,
    );
  }
  return output;
}

function desktopIdentifier(appPath) {
  const identifier = execFileSync(
    PLIST_BUDDY,
    [
      "-c",
      "Print :CFBundleIdentifier",
      join(appPath, "Contents", "Info.plist"),
    ],
    { encoding: "utf8" },
  ).trim();
  if (!/^[A-Za-z0-9][A-Za-z0-9.-]*$/.test(identifier)) {
    throw new Error(`Invalid bundle identifier for ${appPath}`);
  }
  return identifier;
}

function signAdHoc(target, identifier, requirement, entitlements) {
  run([
    "--force",
    "--sign",
    "-",
    "--identifier",
    identifier,
    "-r",
    requirement,
    "--options",
    "runtime",
    "--entitlements",
    entitlements,
    target,
  ]);
}

function daemonBundlePath(outerAppPath) {
  const candidates = [
    join(
      outerAppPath,
      "Contents",
      "Resources",
      "app.asar.unpacked",
      "resources",
      DAEMON_APP_NAME,
    ),
    join(outerAppPath, "Contents", "Resources", DAEMON_APP_NAME),
  ];
  return candidates.find((candidate) => existsSync(candidate)) ?? null;
}

async function pinDesktopIdentity(outerAppPath) {
  const signature = signatureInfo(outerAppPath);
  if (signature !== null && !/^Signature=adhoc\r?$/m.test(signature)) {
    if (/^Authority=.+$/m.test(signature)) {
      log(`certificate signature on ${outerAppPath}, leaving signed code untouched`);
      return;
    }
    throw new Error(`Unrecognized signature for ${outerAppPath}`);
  }

  const entitlements = join(__dirname, "entitlements.mac.plist");
  const identifier = desktopIdentifier(outerAppPath);
  const daemonApp = daemonBundlePath(outerAppPath);
  if (!daemonApp) {
    throw new Error(`Required ${DAEMON_APP_NAME} was not packaged in ${outerAppPath}`);
  }
  const daemonBinary = join(daemonApp, "Contents", "MacOS", DAEMON_BINARY_NAME);
  if (!existsSync(daemonBinary)) {
    throw new Error(`Daemon binary missing at ${daemonBinary}`);
  }

  if (signature === null) {
    // Fork x64 builds can skip electron-builder signing entirely. Seal nested
    // Electron frameworks and helpers before pinning explicit identities.
    run([
      "--force",
      "--deep",
      "--sign",
      "-",
      "--options",
      "runtime",
      "--entitlements",
      entitlements,
      outerAppPath,
    ]);
    log(`added ad-hoc signatures to unsigned app tree ${outerAppPath}`);
  }

  // Sign leaf code before its bundle, then re-seal Multica.app after its
  // Resources were modified.
  signAdHoc(daemonBinary, DAEMON_IDENTIFIER, DAEMON_REQUIREMENT, entitlements);
  signAdHoc(daemonApp, DAEMON_IDENTIFIER, DAEMON_REQUIREMENT, entitlements);
  run(["--verify", "--strict", daemonApp]);

  signAdHoc(
    outerAppPath,
    identifier,
    `=designated => identifier "${identifier}"`,
    entitlements,
  );
  run(["--verify", "--deep", "--strict", outerAppPath]);
  log(
    `pinned stable ${identifier} and ${DAEMON_IDENTIFIER} identities in ${outerAppPath}`,
  );
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
