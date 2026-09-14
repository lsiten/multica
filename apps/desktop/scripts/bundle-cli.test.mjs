// @vitest-environment node

import { execFileSync } from "node:child_process";
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

describe("bundled desktop helper", () => {
  it.skipIf(process.platform === "win32").each([true, false])(
    "removes a stale standalone daemon app when a built helper is available: %s",
    (hasBinary) => {
      const directory = mkdtempSync(join(tmpdir(), "multica-bundle-"));
      try {
        const scripts = join(directory, "apps", "desktop", "scripts");
        const resources = join(directory, "apps", "desktop", "resources");
        const tools = join(directory, "tools");
        const binaryDirectory = join(directory, "server", "bin", "darwin-arm64");
        for (const path of [scripts, tools, binaryDirectory, join(resources, "MulticaDaemon.app")]) {
          mkdirSync(path, { recursive: true });
        }
        for (const name of ["bundle-cli.mjs", "bundle-cli-env.mjs"]) {
          copyFileSync(fileURLToPath(new URL(name, import.meta.url)), join(scripts, name));
        }
        for (const name of ["go", "git", "codesign"]) {
          const path = join(tools, name);
          writeFileSync(path, `#!/bin/sh\nexit ${name === "codesign" ? 0 : 1}\n`);
          chmodSync(path, 0o755);
        }
        if (hasBinary) writeFileSync(join(binaryDirectory, "multica"), "fixture-helper");
        execFileSync(process.execPath, [join(scripts, "bundle-cli.mjs"), "--target-platform", "darwin", "--target-arch", "arm64"], {
          cwd: directory,
          env: { ...process.env, PATH: `${tools}${delimiter}${process.env.PATH ?? ""}` },
          stdio: "pipe",
        });
        expect(existsSync(join(resources, "MulticaDaemon.app"))).toBe(false);
        const bundledBinary = join(resources, "bin", "multica");
        expect(existsSync(bundledBinary)).toBe(hasBinary);
        if (hasBinary) expect(readFileSync(bundledBinary, "utf8")).toBe("fixture-helper");
      } finally {
        rmSync(directory, { recursive: true, force: true });
      }
    },
  );
});
