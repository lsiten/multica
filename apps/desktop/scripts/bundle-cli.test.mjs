// @vitest-environment node

import { execFileSync } from "node:child_process";
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

describe("bundled desktop helper", () => {
  it.skipIf(process.platform === "win32").each([true, false])(
    "creates a stable daemon app bundle when a built helper is available: %s",
    (hasBinary) => {
      const directory = mkdtempSync(join(tmpdir(), "multica-bundle-"));
      try {
        const scripts = join(directory, "apps", "desktop", "scripts");
        const resources = join(directory, "apps", "desktop", "resources");
        const tools = join(directory, "tools");
        const binaryDirectory = join(directory, "server", "bin", "darwin-arm64");
        for (const path of [scripts, tools, binaryDirectory]) {
          mkdirSync(path, { recursive: true });
        }
        mkdirSync(join(resources, "MulticaDaemon.app", "Contents"), { recursive: true });
        for (const name of ["bundle-cli.mjs", "bundle-cli-env.mjs"]) {
          copyFileSync(fileURLToPath(new URL(name, import.meta.url)), join(scripts, name));
        }
        for (const name of ["go", "git", "codesign"]) {
          const path = join(tools, name);
          writeFileSync(path, `#!/bin/sh\nexit ${name === "codesign" ? 0 : 1}\n`);
          chmodSync(path, 0o755);
        }
        if (hasBinary) {
          const bundledSource = join(binaryDirectory, "multica");
          if (process.platform === "darwin") {
            execFileSync("/usr/bin/xcrun", ["clang", "-x", "c", "-o", bundledSource, "-"], {
              input: "int main(void) { return 0; }",
              stdio: ["pipe", "pipe", "pipe"],
            });
          } else {
            writeFileSync(bundledSource, "fixture-helper");
          }
        }
        execFileSync(process.execPath, [join(scripts, "bundle-cli.mjs"), "--target-platform", "darwin", "--target-arch", "arm64"], {
          cwd: directory,
          env: { ...process.env, PATH: `${tools}${delimiter}${process.env.PATH ?? ""}` },
          stdio: "pipe",
        });
        const bundledBinary = join(resources, "bin", "multica");
        const daemonBinary = join(resources, "MulticaDaemon.app", "Contents", "MacOS", "multica");
        const daemonPlist = join(resources, "MulticaDaemon.app", "Contents", "Info.plist");
        const expectsDaemonBundle = hasBinary && process.platform === "darwin";
        expect(existsSync(bundledBinary)).toBe(hasBinary);
        expect(existsSync(daemonBinary)).toBe(expectsDaemonBundle);
        expect(existsSync(daemonPlist)).toBe(expectsDaemonBundle);
        if (hasBinary) expect(readFileSync(bundledBinary)).toBeInstanceOf(Buffer);
        if (expectsDaemonBundle) {
          expect(readFileSync(daemonBinary)).toBeInstanceOf(Buffer);
          const plist = readFileSync(daemonPlist, "utf8");
          expect(plist).toContain("<string>ai.multica.daemon</string>");
          expect(plist).toContain("<string>multica</string>");
          expect(plist).toContain("<key>LSUIElement</key>");
        }
      } finally {
        rmSync(directory, { recursive: true, force: true });
      }
    },
  );
});
