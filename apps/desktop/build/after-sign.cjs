"use strict";

// electron-builder afterSign hook (macOS only).
//
// Why this exists: the bundled Multica daemon (MulticaDaemon.app) needs a
// STABLE TCC identity so Screen Recording permission persists across updates
// and the daemon appears in System Settings -> Screen Recording. The daemon
// is ad-hoc signed with an identifier-based designated requirement at bundle
// time (scripts/bundle-cli.mjs). electron-builder then re-signs every nested
// binary while packaging; in fork builds without a Developer ID certificate
// that re-sign is plain ad-hoc and RESETS the designated requirement to a
// cdhash, which makes macOS treat every rebuilt daemon as a new program.
//
// After packaging finishes signing, this hook:
//   1. detects an ad-hoc outer signature (fork / local builds only),
//   2. re-signs the nested daemon binary and bundle with the stable
//      identifier DR,
//   3. re-seals the outer app (its CodeResources cover the nested code),
//      passing the hardened-runtime entitlements the Electron main process
//      needs (JIT, unsigned executable memory, library validation off).
//
// Developer ID builds are left untouched: electron-builder signs the nested
// bundle using its Info.plist identifier, producing a stable
// identifier + team-id designated requirement automatically, and the
// notarization ticket must cover the tree electron-builder signed.

const { execFileSync, execSync } = require("node:child_process");
const { existsSync, readdirSync } = require("node:fs");
const { join } = require("node:path");

const DAEMON_APP_NAME = "MulticaDaemon.app";
const DAEMON_BIN_NAME = "multica";
const DAEMON_IDENTIFIER = "ai.multica.daemon";
const DESIGNATED_REQUIREMENT = `=designated => identifier "${DAEMON_IDENTIFIER}"`;
const CODESIGN = "/usr/bin/codesign";

function log(message) {
  console.log(`[after-sign] ${message}`);
}

function run(args) {
  execFileSync(CODESIGN, args, { stdio: "pipe" });
}

function signatureInfo(appPath) {
  try {
    return execSync(`${CODESIGN} -dv --verbose=2 ${JSON.stringify(appPath)} 2>&1`, {
      encoding: "utf8",
    });
  } catch {
    return "";
  }
}

async function pinDaemonIdentity(outerAppPath) {
  const info = signatureInfo(outerAppPath);
  if (!/adhoc/i.test(info)) {
    log(`Developer ID signature on ${outerAppPath}, leaving nested code untouched`);
    return;
  }

  const daemonCandidates = [
    join(outerAppPath, "Contents", "Resources", "app.asar.unpacked", "resources", DAEMON_APP_NAME),
    join(outerAppPath, "Contents", "Resources", DAEMON_APP_NAME),
  ];
  const daemonApp = daemonCandidates.find((candidate) => existsSync(candidate));
  if (!daemonApp) {
    log(`no ${DAEMON_APP_NAME} found inside ${outerAppPath}, nothing to pin`);
    return;
  }
  const daemonBinary = join(daemonApp, "Contents", "MacOS", DAEMON_BIN_NAME);
  if (!existsSync(daemonBinary)) {
    log(`daemon binary missing at ${daemonBinary}, skipping`);
    return;
  }

  // Re-sign the inner binary first, then its .app wrapper, with a DR keyed on
  // the stable identifier instead of the per-build cdhash.
  for (const target of [daemonBinary, daemonApp]) {
    run([
      "--force",
      "--sign",
      "-",
      "--identifier",
      DAEMON_IDENTIFIER,
      "-r",
      DESIGNATED_REQUIREMENT,
      target,
    ]);
  }

  // Modifying nested code invalidates the outer bundle seal. Re-sign the app
  // bundle (codesign regenerates a self-consistent cdhash DR and CodeResources)
  // with the hardened-runtime entitlements Electron needs.
  const entitlements = join(__dirname, "entitlements.mac.plist");
  run([
    "--force",
    "--sign",
    "-",
    "--options",
    "runtime",
    "--entitlements",
    entitlements,
    outerAppPath,
  ]);

  execFileSync(CODESIGN, ["--verify", "--deep", "--strict", outerAppPath], {
    stdio: "pipe",
  });
  log(`pinned stable ${DAEMON_IDENTIFIER} identity inside ${outerAppPath}`);
}

exports.default = async function afterSign(context) {
  if (!context || context.electronPlatformName !== "darwin") return;
  const appOutDir = context.appOutDir;
  if (!appOutDir || !existsSync(appOutDir)) return;

  const outerApps = readdirSync(appOutDir).filter((name) => name.endsWith(".app"));
  for (const name of outerApps) {
    await pinDaemonIdentity(join(appOutDir, name));
  }
};
