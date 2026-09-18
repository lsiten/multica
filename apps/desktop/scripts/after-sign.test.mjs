// @vitest-environment node

import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { runInNewContext } from "node:vm";
import { afterEach, describe, expect, it } from "vitest";

const require = createRequire(import.meta.url);
const hookPath = fileURLToPath(new URL("../build/after-sign.cjs", import.meta.url));
const source = readFileSync(hookPath, "utf8");
const directories = [];

function fixture() {
  const directory = mkdtempSync(join(tmpdir(), "multica-signing-"));
  directories.push(directory);
  const app = join(directory, "Multica $QA.app");
  mkdirSync(join(app, "Contents", "MacOS"), { recursive: true });
  return { directory, app };
}

function mockedHook(signature, status = 0) {
  const calls = [];
  const exports = {};
  runInNewContext(source, {
    exports,
    __dirname: dirname(hookPath),
    console: { log() {} },
    require(name) {
      if (name !== "node:child_process") return require(name);
      return {
        execSync: () => signature,
        spawnSync: (command, args) => {
          calls.push({ command, args });
          return { status, stdout: "", stderr: signature };
        },
        execFileSync: (command, args) => {
          calls.push({ command, args });
          if (command.endsWith("PlistBuddy")) return "ai.multica.desktop\n";
          return "";
        },
      };
    },
  });
  return { afterSign: exports.default, calls };
}

afterEach(() => {
  for (const directory of directories.splice(0)) {
    rmSync(directory, { recursive: true, force: true });
  }
});

describe("afterSign screen recording identity", () => {
  it("pins the containing app identity without requiring a daemon app", async () => {
    const { directory, app } = fixture();
    const { afterSign, calls } = mockedHook("Identifier=ai.multica.desktop\nSignature=adhoc\n");
    await afterSign({ electronPlatformName: "darwin", appOutDir: directory });

    const signing = calls.filter(({ args }) => args.includes("--force"));
    expect(signing).toHaveLength(1);
    expect(signing[0].args).toEqual([
      "--force", "--sign", "-", "--identifier", "ai.multica.desktop",
      "-r", '=designated => identifier "ai.multica.desktop"',
      "--options", "runtime", "--entitlements",
      join(dirname(hookPath), "entitlements.mac.plist"), app,
    ]);
    expect(calls.at(-1).args).toEqual(["--verify", "--deep", "--strict", app]);
  });

  it("bootstraps only explicitly unsigned app trees before pinning and verification", async () => {
    const { directory, app } = fixture();
    const { afterSign, calls } = mockedHook(`${app}: code object is not signed at all\n`, 1);
    await afterSign({ electronPlatformName: "darwin", appOutDir: directory });
    const signing = calls.filter(({ args }) => args.includes("--force"));
    expect(signing).toHaveLength(2);
    expect(signing[0].args).toEqual(["--force", "--deep", "--sign", "-", "--options", "runtime", "--entitlements", join(dirname(hookPath), "entitlements.mac.plist"), app]);
    expect(signing[0].args).not.toContain("--identifier");
    expect(signing[1].args).toContain(`=designated => identifier "ai.multica.desktop"`);
    expect(calls.at(-1).args).toEqual(["--verify", "--deep", "--strict", app]);
  });

  it.skipIf(process.platform !== "darwin")("signs a truly unsigned x64 app and nested helper without executing either", async () => {
    const { directory, app } = fixture();
    const nested = join(app, "Contents", "Frameworks", "Nested Helper.app");
    for (const [bundle, executable, identifier] of [[app, "Multica", "ai.multica.desktop.signing-test"], [nested, "Helper", "ai.multica.desktop.signing-test.helper"]]) {
      mkdirSync(join(bundle, "Contents", "MacOS"), { recursive: true });
      writeFileSync(join(bundle, "Contents", "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${identifier}</string><key>CFBundleExecutable</key><string>${executable}</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`);
      execFileSync("/usr/bin/xcrun", ["clang", "-arch", "x86_64", "-x", "c", "-o", join(bundle, "Contents", "MacOS", executable), "-"], {input: "int main(void) { return 0; }", stdio: ["pipe", "pipe", "pipe"]});
    }
    const before = spawnSync("/usr/bin/codesign", ["-dv", app], {encoding:"utf8"});
    expect(before.status).not.toBe(0);
    expect(before.stderr).toContain("code object is not signed at all");
    await require(hookPath).default({electronPlatformName:"darwin",appOutDir:directory});
    for (const [bundle, identifier] of [[app,"ai.multica.desktop.signing-test"],[nested,"ai.multica.desktop.signing-test.helper"]]) {
      const after=spawnSync("/usr/bin/codesign",["-d","--verbose=4",bundle],{encoding:"utf8"});
      expect(after.status).toBe(0);expect(after.stdout+after.stderr).toContain("Signature=adhoc");expect(after.stdout+after.stderr).toContain(`Identifier=${identifier}`);
      execFileSync("/usr/bin/codesign",["--verify","--deep","--strict",bundle],{stdio:"pipe"});
    }
  }, 30_000);

  it("leaves Developer ID signatures and their nested code untouched", async () => {
    const { directory } = fixture();
    const { afterSign, calls } = mockedHook(
      "Identifier=ai.multica.desktop\nAuthority=Developer ID Application: Example (TEAM)\nTeamIdentifier=TEAM\n",
    );
    await afterSign({ electronPlatformName: "darwin", appOutDir: directory });
    expect(calls.some(({ args }) => args.includes("--force"))).toBe(false);
  });

  it.each([
    ["", 0],
    ["codesign failed", 1],
  ])("fails explicitly when signature inspection is unknown (%j)", async (signature, status) => {
    const { directory } = fixture();
    const { afterSign } = mockedHook(signature, status);
    await expect(afterSign({ electronPlatformName: "darwin", appOutDir: directory })).rejects.toThrow(/signature/i);
  });

  it.skipIf(process.platform !== "darwin")("keeps the real main-app designated requirement stable across rebuilt executables", async () => {
    const { directory, app } = fixture();
    const identifier = "ai.multica.desktop.signing-test";
    const binary = join(app, "Contents", "MacOS", "Multica");
    writeFileSync(join(app, "Contents", "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>${identifier}</string>
<key>CFBundleExecutable</key><string>Multica</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>`);
    const afterSign = require(hookPath).default;
    const requirements = [];
    const hashes = [];
    for (const exitCode of [0, 1]) {
      execFileSync("/usr/bin/xcrun", ["clang", "-x", "c", "-o", binary, "-"], {
        input: `int main(void) { return ${exitCode}; }`,
        stdio: ["pipe", "pipe", "pipe"],
      });
      execFileSync("/usr/bin/codesign", ["--force", "--sign", "-", app], { stdio: "pipe" });
      await afterSign({ electronPlatformName: "darwin", appOutDir: directory });
      const inspection = spawnSync("/usr/bin/codesign", ["-d", "-r-", "--verbose=4", app], { encoding: "utf8" });
      expect(inspection.status).toBe(0);
      const output = inspection.stdout + inspection.stderr;
      expect(output).toMatch(/flags=.*runtime/);
      requirements.push(output.match(/^designated => (.+)$/m)?.[1]);
      hashes.push(output.match(/^CDHash=(.+)$/m)?.[1]);
      const entitlements = spawnSync("/usr/bin/codesign", ["-d", "--entitlements", ":-", app], { encoding: "utf8" });
      expect(entitlements.status).toBe(0);
      for (const capability of ["allow-jit", "allow-unsigned-executable-memory", "disable-library-validation"]) {
        expect(entitlements.stdout + entitlements.stderr).toContain(`com.apple.security.cs.${capability}`);
      }
      execFileSync("/usr/bin/codesign", ["--verify", "--deep", "--strict", app], { stdio: "pipe" });
    }
    expect(requirements).toEqual([`identifier "${identifier}"`, `identifier "${identifier}"`]);
    expect(hashes[0]).toBeTruthy();
    expect(hashes[0]).not.toBe(hashes[1]);
  }, 30_000);
});
