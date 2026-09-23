import { app } from "electron";
import { copyFile, mkdir, readFile, rename, writeFile } from "fs/promises";
import { randomUUID } from "node:crypto";
import { extname, join } from "path";
import {
  DEFAULT_RUNTIME_CONFIG,
  parseRuntimeConfig,
  runtimeConfigFromDevEnv,
  type RuntimeConfig,
  type RuntimeConfigEnv,
  type RuntimeConfigResult,
} from "../shared/runtime-config";

export async function loadRuntimeConfig(options: {
  isDev: boolean;
  env: RuntimeConfigEnv;
  configPath?: string;
}): Promise<RuntimeConfigResult> {
  if (options.isDev) {
    try {
      return { ok: true, config: runtimeConfigFromDevEnv(options.env) };
    } catch (err) {
      return { ok: false, error: { message: errorMessage(err) } };
    }
  }

  const configPath = options.configPath ?? desktopConfigPath();
  try {
    const raw = await readFile(configPath, "utf-8");
    return { ok: true, config: parseRuntimeConfig(raw) };
  } catch (err) {
    if (isMissingFileError(err)) {
      return { ok: true, config: { ...DEFAULT_RUNTIME_CONFIG } };
    }
    return {
      ok: false,
      error: {
        message: `Invalid ${configPath}: ${errorMessage(err)}`,
      },
    };
  }
}

export function desktopConfigPath(): string {
  return join(app.getPath("home"), ".multica", "desktop.json");
}

export async function saveRuntimeConfig(config: RuntimeConfig): Promise<void> {
  const path = desktopConfigPath();
  const directory = join(app.getPath("home"), ".multica");
  await mkdir(directory, { recursive: true });
  let stored = config;
  if (config.iconPath) {
    const iconPath = join(directory, `desktop-icon-${randomUUID()}${extname(config.iconPath)}`);
    await copyFile(config.iconPath, iconPath);
    stored = { ...config, iconPath };
  }
  const temporary = `${path}.${randomUUID()}.tmp`;
  await writeFile(temporary, JSON.stringify(stored, null, 2) + "\n", { mode: 0o600 });
  await rename(temporary, path);
}

function isMissingFileError(err: unknown): boolean {
  return Boolean(
    err &&
      typeof err === "object" &&
      "code" in err &&
      (err as NodeJS.ErrnoException).code === "ENOENT",
  );
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export type { RuntimeConfig, RuntimeConfigResult };
