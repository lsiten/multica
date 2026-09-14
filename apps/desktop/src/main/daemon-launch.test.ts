// @vitest-environment node
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { readDaemonParentPid, startMacDaemon } from "./daemon-launch";

const directories: string[] = [];
const children: number[] = [];

afterEach(async () => {
  for (const pid of children.splice(0)) {
    try { process.kill(pid, "SIGTERM"); } catch (error) {
      if (!(error instanceof Error) || !("code" in error) || error.code !== "ESRCH") throw error;
    }
  }
  await Promise.all(directories.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

async function fixture(body: string) {
  const dir = await mkdtemp(join(tmpdir(), "multica-daemon-launch-"));
  directories.push(dir);
  const binary = join(dir, "multica");
  await writeFile(binary, `#!/bin/sh\n${body}\n`, { mode: 0o755 });
  return { dir, binary };
}

describe.skipIf(process.platform === "win32")("macOS daemon launch", () => {
  it("keeps a foreground daemon as a direct child and waits for readiness", async () => {
    const { dir, binary } = await fixture('printf "%s\\n" "$@"\nprintf "%s" "$MULTICA_LAUNCHED_BY" >&2\nexec /bin/sleep 30');
    let checked = false;
    await startMacDaemon({
      binary, profile: "desktop-test", directory: dir,
      env: { ...process.env, MULTICA_LAUNCHED_BY: "desktop" },
      isReady: async (pid) => {
        if (!children.includes(pid)) children.push(pid);
        expect(await readDaemonParentPid(pid)).toBe(process.pid);
        expect(await readFile(join(dir, "daemon.pid"), "utf8")).toBe(String(pid));
        checked = true;
        return (await readFile(join(dir, "daemon.err.log"), "utf8")).endsWith("desktop");
      },
    });
    expect(checked).toBe(true);
    const output = await readFile(join(dir, "daemon.err.log"), "utf8");
    expect(output).toContain("daemon\nstart\n--foreground\n--profile\ndesktop-test\n");
    expect(output).toContain("desktop");
  });

  it("reports early exit and preserves stderr instead of reporting a successful start", async () => {
    const { dir, binary } = await fixture('printf "startup rejected" >&2\nexit 23');
    await expect(startMacDaemon({
      binary, profile: "desktop-test", directory: dir, env: process.env,
      isReady: async () => false,
    })).rejects.toThrow("23");
    expect(await readFile(join(dir, "daemon.err.log"), "utf8")).toContain("startup rejected");
  });

  it("reports a missing bundled binary without leaving a readiness poll running", async () => {
    const { dir } = await fixture("exit 0");
    await expect(startMacDaemon({
      binary: join(dir, "missing"), profile: "desktop-test", directory: dir,
      env: process.env, isReady: async () => false,
    })).rejects.toThrow("ENOENT");
  });

  it("preserves the previous crash log when starting after it reaches the CLI log limit", async () => {
    const { dir, binary } = await fixture("exec /bin/sleep 30");
    const previous = "x".repeat(5 * 1024 * 1024);
    await writeFile(join(dir, "daemon.err.log"), previous);
    await startMacDaemon({
      binary, profile: "desktop-test", directory: dir, env: process.env,
      isReady: async (pid) => { children.push(pid); return true; },
    });
    expect(await readFile(join(dir, "daemon.err.log.1"), "utf8")).toBe(previous);
  });

  it("rejects malformed health PIDs without running a process lookup", async () => {
    for (const pid of [undefined, 0, -1, NaN, 1.5, Infinity]) {
      expect(await readDaemonParentPid(pid)).toBeNull();
    }
  });
});
