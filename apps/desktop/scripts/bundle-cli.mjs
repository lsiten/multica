#!/usr/bin/env node
// Builds the `multica` CLI from server/cmd/multica and copies the binary
// into apps/desktop/resources/bin/ so electron-vite (dev) and electron-
// builder (prod) pick it up. Running this on every dev/build/package
// invocation guarantees the bundled CLI always matches the current Go
// source — no more stale binary surprises. Go's build cache makes the
// no-op case (nothing changed) effectively free.
//
// ldflags mirror `make build` so `multica --version` reports a meaningful
// version / commit / date.
//
// Graceful: if `go` is not installed (e.g. frontend-only contributor), we
// skip the build and fall through to auto-install at runtime. A genuine
// Go compile error is fatal — you want that to block dev, not hide.

import { access, chmod, copyFile, mkdir, rm, writeFile } from "node:fs/promises";
import { constants } from "node:fs";
import { execFileSync, execSync } from "node:child_process";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { cgoEnabledForGoos } from "./bundle-cli-env.mjs";

const here = dirname(fileURLToPath(import.meta.url));

// Stable codesign identifier for the bundled daemon; keep in sync with the
// macOS packaging step. Screen Recording permission is bound to this identity.
const DAEMON_SIGN_IDENTIFIER = "ai.multica.daemon";
const repoRoot = resolve(here, "..", "..", "..");
const serverDir = join(repoRoot, "server");

const PLATFORM_TO_GOOS = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows",
};

const SUPPORTED_ARCHS = new Set(["x64", "arm64"]);

function runtimePlatformFromArgs(argv) {
  const flagIndex = argv.indexOf("--target-platform");
  if (flagIndex === -1) return process.platform;
  return argv[flagIndex + 1] ?? "";
}

function runtimeArchFromArgs(argv) {
  const flagIndex = argv.indexOf("--target-arch");
  if (flagIndex === -1) return process.arch;
  return argv[flagIndex + 1] ?? "";
}

function normalizeRuntimePlatform(platform) {
  if (platform in PLATFORM_TO_GOOS) return platform;
  throw new Error(
    `[bundle-cli] unsupported target platform: ${platform}. ` +
      "Use darwin, linux, or win32.",
  );
}

function normalizeRuntimeArch(arch) {
  if (SUPPORTED_ARCHS.has(arch)) return arch;
  throw new Error(
    `[bundle-cli] unsupported target architecture: ${arch}. ` +
      "Use x64 or arm64.",
  );
}

function binaryNameForPlatform(platform) {
  return platform === "win32" ? "multica.exe" : "multica";
}

const targetPlatform = normalizeRuntimePlatform(
  runtimePlatformFromArgs(process.argv.slice(2)),
);
const targetArch = normalizeRuntimeArch(runtimeArchFromArgs(process.argv.slice(2)));
const goos = PLATFORM_TO_GOOS[targetPlatform];
const goarch = targetArch === "x64" ? "amd64" : targetArch;
const binName = binaryNameForPlatform(targetPlatform);
const srcBinary = join(serverDir, "bin", `${goos}-${goarch}`, binName);
const destDir = join(repoRoot, "apps", "desktop", "resources", "bin");
const destBinary = join(destDir, binName);

// Hand git arguments straight to the binary (no shell). A match pattern like
// `v[0-9]*` must reach git as one literal argument; routing it through a shell
// string breaks on Windows, where cmd.exe keeps the POSIX single quotes and
// git matches no tag — degrading the bundled CLI's version to the
// 0.0.0-g<hash> fallback.
function git(...args) {
  try {
    return execFileSync("git", args, { encoding: "utf-8" }).trim();
  } catch {
    return "";
  }
}

function hasGo() {
  try {
    execSync("go version", { stdio: "pipe" });
    return true;
  } catch {
    return false;
  }
}

async function exists(p) {
  try {
    await access(p, constants.F_OK);
    return true;
  } catch {
    return false;
  }
}

if (hasGo()) {
  const version =
    git("describe", "--tags", "--match", "v[0-9]*", "--always", "--dirty") ||
    "dev";
  const commit = git("rev-parse", "--short", "HEAD") || "unknown";
  const date = new Date().toISOString().replace(/\.\d+Z$/, "Z");
  const ldflags = `-X main.version=${version} -X main.commit=${commit} -X main.date=${date}`;

  console.log(
    `[bundle-cli] go build → ${srcBinary} (${goos}/${goarch}, version=${version} commit=${commit})`,
  );
  await mkdir(join(serverDir, "bin", `${goos}-${goarch}`), { recursive: true });
  execFileSync(
    "go",
    [
      "build",
      "-ldflags",
      ldflags,
      "-o",
      srcBinary,
      "./cmd/multica",
    ],
    {
      cwd: serverDir,
      stdio: "inherit",
      env: {
        ...process.env,
        CGO_ENABLED: cgoEnabledForGoos(goos),
        GOOS: goos,
        GOARCH: goarch,
      },
    },
  );
} else {
  console.warn(
    "[bundle-cli] `go` not found in PATH — skipping CLI build. " +
      "Desktop will use whatever is already in resources/bin/, or fall back " +
      "to auto-installing the latest release at runtime.",
  );
}

if (!(await exists(srcBinary))) {
  console.warn(
    `[bundle-cli] ${srcBinary} not present — Desktop will fall back to ` +
      `auto-installing the latest release at runtime.`,
  );
  await rm(destDir, { recursive: true, force: true });
  process.exit(0);
}

await rm(destDir, { recursive: true, force: true });
await mkdir(destDir, { recursive: true });
// Remove a macOS daemon bundle left by an earlier darwin target in the same
// multi-platform package run so it never leaks into linux/windows installers.
await rm(join(repoRoot, "apps", "desktop", "resources", "MulticaDaemon.app"), {
  recursive: true,
  force: true,
});
await copyFile(srcBinary, destBinary);
await chmod(destBinary, 0o755);

// macOS: ad-hoc sign so Gatekeeper doesn't complain when the parent app
// (which itself may be unsigned in dev) spawns the child. A STABLE identifier
// is critical: TCC keys Screen Recording permission on the executable's
// signing identity. Without --identifier the ad-hoc identity is a cdhash
// (multica-<hash>), so every Go rebuild looked like a brand-new program and
// macOS re-prompted even though the user had already granted permission.
//
// The fixed identifier alone is not enough for a bare executable: TCC cannot
// attach a display entry to a cdhash-identified binary living in resources/bin,
// so the daemon never appears in System Settings and the grant does not stick.
// Ship the binary inside its own MulticaDaemon.app bundle (bundle id
// ai.multica.daemon, LSUIElement background agent); Desktop spawns the binary
// at Contents/MacOS/multica, which gives TCC a stable bundle identity.
if (goos === "darwin" && process.platform === "darwin") {
  signDaemonBinary(destBinary);
  const appBundle = await buildDaemonAppBundle(destBinary);
  signDaemonApp(appBundle);
}

console.log(`[bundle-cli] bundled ${srcBinary} → ${destBinary}`);

function signDaemonBinary(binaryPath) {
  if (process.platform !== "darwin") return;
  try {
    // Stable identifier so TCC keys Screen Recording by ai.multica.daemon
    // rather than a per-build cdhash. execFileSync (no shell) keeps the
    // identifier quoting exact.
    execFileSync(
      "codesign",
      [
        "--force", "--sign", "-",
        "--identifier", DAEMON_SIGN_IDENTIFIER,
        // Identifier-based designated requirement (instead of the ad-hoc
        // default cdhash DR) so TCC recognises rebuilds as the same program.
        "-r", `=designated => identifier "${DAEMON_SIGN_IDENTIFIER}"`,
        binaryPath,
      ],
      { stdio: "pipe" },
    );
  } catch (error) {
    console.warn(`[bundle-cli] codesign failed for ${binaryPath}:`, error?.message ?? error);
  }
}

async function buildDaemonAppBundle(binaryPath) {
  const bundleRoot = join(destDir, "..", "MulticaDaemon.app");
  const contentsDir = join(bundleRoot, "Contents");
  const macosDir = join(contentsDir, "MacOS");
  await rm(bundleRoot, { recursive: true, force: true });
  await mkdir(macosDir, { recursive: true });
  const appBinary = join(macosDir, binName);
  await copyFile(binaryPath, appBinary);
  await chmod(appBinary, 0o755);
  const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key>
  <string>Multica Daemon</string>
  <key>CFBundleDisplayName</key>
  <string>Multica Daemon</string>
  <key>CFBundleIdentifier</key>
  <string>${DAEMON_SIGN_IDENTIFIER}</string>
  <key>CFBundleExecutable</key>
  <string>${binName}</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>${bundleVersion()}</string>
  <key>CFBundleVersion</key>
  <string>${bundleVersion()}</string>
  <key>LSUIElement</key>
  <true/>
  <key>LSMinimumSystemVersion</key>
  <string>11.0</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
`;
  await writeFile(join(contentsDir, "Info.plist"), plist, { mode: 0o644 });
  return bundleRoot;
}

function bundleVersion() {
  const fromGit = git("describe", "--tags", "--match", "v[0-9]*", "--abbrev=0").replace(/^v/, "");
  return /^[0-9]+\.[0-9]+\.[0-9]+/.test(fromGit) ? fromGit : "0.0.0";
}

function signDaemonApp(appBundlePath) {
  if (process.platform !== "darwin") return;
  try {
    // Sign the whole bundle. The nested binary already carries the stable
    // ai.multica.daemon identifier; the Info.plist bundle id matches it so
    // TCC treats the daemon as one stable program across rebuilds.
    execFileSync(
      "codesign",
      [
        "--force", "--deep", "--sign", "-",
        "--identifier", DAEMON_SIGN_IDENTIFIER,
        "-r", `=designated => identifier "${DAEMON_SIGN_IDENTIFIER}"`,
        appBundlePath,
      ],
      { stdio: "pipe" },
    );
  } catch (error) {
    console.warn(`[bundle-cli] codesign failed for ${appBundlePath}:`, error?.message ?? error);
  }
}
