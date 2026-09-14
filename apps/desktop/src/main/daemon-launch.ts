import { execFile, spawn } from "node:child_process";
import { mkdir, open, rename, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);
const STARTUP_TIMEOUT_MS = 45_000;
// Match the standalone CLI's openBoundedErrLog policy.
const STDERR_LOG_LIMIT = 5 * 1024 * 1024;

interface MacDaemonLaunch {
  readonly binary: string;
  readonly profile: string;
  readonly directory: string;
  readonly env: NodeJS.ProcessEnv;
  readonly isReady: (pid: number, signal: AbortSignal) => Promise<boolean>;
}

class DaemonLaunchError extends Error {}

/**
 * Launches the macOS helper under Electron's responsibility. The CLI's normal
 * background start re-execs and detaches; using its foreground entry directly
 * also makes an old daemon from a previous Desktop launch identifiable by PPID.
 * File-backed stdio and unref let it survive Desktop exit when autoStop is off.
 */
export async function startMacDaemon(options: MacDaemonLaunch): Promise<void> {
  await mkdir(options.directory, { recursive: true });
  const logPath = join(options.directory, "daemon.err.log");
  try {
    if ((await stat(logPath)).size >= STDERR_LOG_LIMIT) await rename(logPath, `${logPath}.1`);
  } catch (error) {
    if (!(error instanceof Error)) throw error;
    if (!("code" in error) || error.code !== "ENOENT") {
      console.warn("[daemon] could not rotate daemon crash log:", error.message);
    }
  }
  const log = await open(logPath, "a", 0o600);
  try {
    const child = spawn(options.binary, [
      "daemon", "start", "--foreground", "--profile", options.profile,
    ], {
      env: options.env,
      detached: false,
      stdio: ["ignore", log.fd, log.fd],
    });
    const controller = new AbortController();
    try {
      await new Promise<void>((resolve, reject) => {
        let settled = false;
        const finish = (error?: Error) => {
          if (settled) return;
          settled = true;
          controller.abort();
          child.unref();
          if (error) reject(error);
          else resolve();
        };
        child.once("error", finish);
        child.once("exit", (code, signal) => finish(new DaemonLaunchError(
          `Daemon exited before becoming ready (${signal ?? code}). Check ${logPath}`,
        )));
        child.once("spawn", () => {
          const pid = child.pid;
          if (!pid) {
            finish(new DaemonLaunchError("Daemon started without a process ID"));
            return;
          }
          const awaitReadiness = async () => {
            // Recovery must see a still-booting child before /health is bound.
            await writeFile(join(options.directory, "daemon.pid"), String(pid));
            const deadline = Date.now() + STARTUP_TIMEOUT_MS;
            while (!controller.signal.aborted && Date.now() < deadline) {
              if (await options.isReady(pid, controller.signal)) break;
              await delay(500, undefined, { signal: controller.signal });
            }
            // Like CLI background start, leave a slow healthy child alive;
            // daemon-manager's health polling continues to show "starting".
            finish();
          };
          void awaitReadiness().catch((error: unknown) => {
            if (controller.signal.aborted) return;
            // A failed PID write/readiness check must not leave a second,
            // untracked helper behind when the user retries the start.
            child.kill("SIGTERM");
            finish(error instanceof Error ? error : new DaemonLaunchError(String(error)));
          });
        });
      });
    } finally {
      controller.abort();
    }
  } finally {
    await log.close();
  }
}

/** Reads the live parent without treating a failed lookup as proof of an orphan. */
export async function readDaemonParentPid(pid: number | undefined): Promise<number | null> {
  if (typeof pid !== "number" || !Number.isSafeInteger(pid) || pid <= 0) return null;
  try {
    const { stdout } = await execFileAsync("/bin/ps", ["-p", String(pid), "-o", "ppid="], {
      timeout: 2_000,
    });
    const value = stdout.trim();
    return /^\d+$/.test(value) && Number(value) > 0 ? Number(value) : null;
  } catch (error) {
    if (!(error instanceof Error)) throw error;
    console.warn("[daemon] could not read daemon parent process:", error.message);
    return null;
  }
}
