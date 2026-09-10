import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const workflow = readFileSync(join(root, ".github/workflows/fork-desktop.yml"), "utf8")
  .replaceAll("\r\n", "\n");

function stepScript(name) {
  const block = workflow.split(`      - name: ${name}\n`)[1]?.split("\n      - ")[0];
  assert.ok(block, `Missing workflow step: ${name}`);
  const match = block.match(/^        run: (.*)$/m);
  assert.ok(match, `Missing run command: ${name}`);
  return match[1] === "|"
    ? block.slice(match.index + match[0].length + 1).replace(/^          /gm, "")
    : match[1];
}

function fixtureDirectory(t) {
  const directory = mkdtempSync(join(tmpdir(), "multica-desktop-release-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}

test("packaging omits an absent signing certificate and runs through pnpm", (t) => {
  const directory = fixtureDirectory(t);
  const probe = join(directory, "probe.cjs");
  const output = join(directory, "environment.json");
  writeFileSync(probe, `
const fs = require("node:fs");
fs.writeFileSync(process.env.PROBE_OUTPUT, JSON.stringify({
  command: process.argv[2],
  args: process.argv.slice(3),
  certificate: process.env.CSC_LINK,
  password: process.env.CSC_KEY_PASSWORD,
  discovery: process.env.CSC_IDENTITY_AUTO_DISCOVERY,
}));
`);
  const script = `
node() { "$PROBE_NODE" "$PROBE_SCRIPT" node "$@"; }
pnpm() { "$PROBE_NODE" "$PROBE_SCRIPT" pnpm "$@"; }
${stepScript("Package desktop installers").replaceAll("${{ matrix.target }}", "mac")}
`;
  for (const certificate of ["", "test-certificate.p12"]) {
    const result = spawnSync("bash", ["-e", "-o", "pipefail", "-c", script], {
      cwd: directory,
      encoding: "utf8",
      env: {
        ...process.env,
        PROBE_NODE: process.execPath.replaceAll("\\", "/"),
        PROBE_SCRIPT: probe.replaceAll("\\", "/"),
        PROBE_OUTPUT: output,
        CSC_LINK: certificate,
        CSC_KEY_PASSWORD: certificate ? "test-password" : "",
      },
    });
    assert.equal(result.status, 0, result.stderr);
    const observed = JSON.parse(readFileSync(output, "utf8"));
    assert.equal(observed.certificate, certificate || undefined);
    assert.equal(observed.password, certificate ? "test-password" : undefined);
    if (!certificate) assert.equal(observed.discovery, "false");
    assert.equal(observed.command, "pnpm");
    assert.deepEqual(observed.args, ["run", "package", "--", "--mac", "--x64", "--arm64", "--publish", "never"]);
  }
});

test("installer validation accepts actual Linux package architecture names and rejects missing files", (t) => {
  const directory = fixtureDirectory(t);
  const installers = [
    ["x64", "x86_64.AppImage"], ["x64", "amd64.deb"], ["x64", "x86_64.rpm"],
    ["arm64", "arm64.AppImage"], ["arm64", "arm64.deb"], ["arm64", "aarch64.rpm"],
  ];
  for (const [arch, suffix] of installers) {
    const output = join(directory, `apps/desktop/dist/linux-${arch}`);
    mkdirSync(output, { recursive: true });
    writeFileSync(join(output, `multica-desktop-1.2.3-linux-${suffix}`), "installer fixture");
    writeFileSync(join(output, `latest-linux${arch === "arm64" ? "-arm64" : ""}.yml`), "version: 1.2.3\n");
  }
  const check = () => spawnSync("bash", ["-e", "-o", "pipefail", "-c", stepScript("Check installers and update metadata")], {
    cwd: directory,
    encoding: "utf8",
    env: { ...process.env, TARGET: "linux", RELEASE_TAG: "v1.2.3" },
  });
  const result = check();
  assert.equal(result.status, 0, `Valid Linux installers rejected: ${result.stdout}${result.stderr}`);

  rmSync(join(directory, "apps/desktop/dist/linux-arm64/multica-desktop-1.2.3-linux-aarch64.rpm"));
  assert.notEqual(check().status, 0, "Missing RPM must block release");
});

test("release ignores duplicate builder diagnostics while preserving distributable collision checks", (t) => {
  for (const duplicateInstaller of [false, true]) {
    const directory = fixtureDirectory(t);
    for (const arch of ["x64", "arm64"]) {
      const output = join(directory, "artifacts", `mac-${arch}`);
      mkdirSync(output, { recursive: true });
      writeFileSync(join(output, "builder-debug.yml"), "build diagnostics\n");
      const installerArch = duplicateInstaller ? "arm64" : arch;
      writeFileSync(join(output, `multica-desktop-1.2.3-mac-${installerArch}.zip`), "installer fixture");
      writeFileSync(join(output, arch === "x64" ? "latest-x64-mac.yml" : "latest-mac.yml"), "version: 1.2.3\n");
    }
    const calls = join(directory, "github-calls");
    const result = spawnSync("bash", ["-e", "-o", "pipefail", "-c", `
gh() { printf '%s\\n' "$2" >> "$GH_TEST_LOG"; [[ "$2" != view ]]; }
${stepScript("Publish complete desktop release")}
`], {
      cwd: directory,
      encoding: "utf8",
      env: {
        ...process.env,
        GH_TEST_LOG: calls.replaceAll("\\", "/"),
        GH_REPO: "test/desktop",
        RELEASE_TAG: "v1.2.3",
        PRERELEASE: "false",
        GITHUB_STEP_SUMMARY: join(directory, "summary").replaceAll("\\", "/"),
      },
    });
    if (duplicateInstaller) {
      assert.notEqual(result.status, 0, "Duplicate installer must block publication");
    } else {
      assert.equal(result.status, 0, result.stdout + result.stderr);
      assert.deepEqual(readFileSync(calls, "utf8").trim().split("\n"), ["view", "create", "upload", "edit"]);
      assert.equal(existsSync(join(directory, "release-assets/builder-debug.yml")), false);
    }
  }
});
