import { constants } from "node:fs";
import { lstat, open, realpath } from "node:fs/promises";
import { join } from "node:path";
import { credentialSchema, healthSchema, resultSchema } from "@multica/core/types/vscreen-desktop";
import type { VscreenDesktopResult } from "../shared/vscreen-desktop";

export async function readVscreenCredential(directory: string) {
  const uid = process.getuid?.();
  const root = await lstat(directory);
  if (uid === undefined || !root.isDirectory() || root.isSymbolicLink() || root.uid !== uid || (root.mode & 0o022) !== 0 || await realpath(directory) !== directory) throw new Error("local_owner_required");
  directory = join(directory, ".desktop-vscreen");
  const parent = await lstat(directory);
  if (uid === undefined || !parent.isDirectory() || parent.isSymbolicLink() || parent.uid !== uid || (parent.mode & 0o077) !== 0) throw new Error("local_owner_required");
  const file = await open(join(directory, "credential.json"), constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const info = await file.stat();
    if (!info.isFile() || info.uid !== uid || (info.mode & 0o077) !== 0 || info.size > 4096) throw new Error("local_owner_required");
    return credentialSchema.parse(JSON.parse(await file.readFile("utf8")));
  } finally { await file.close(); }
}
export async function requestVscreenDesktop(input: {
  directory: string; port: number; profile: string; backend: string; accountId: string;
  workspaceId?: string; runtimeId?: string; body: Readonly<Record<string, unknown>>;
  isCurrent: () => boolean; profileAccount: () => Promise<string | null>;
  transport?: typeof fetch;
}): Promise<VscreenDesktopResult> {
  const transport = input.transport ?? fetch;
  if (!input.isCurrent() || await input.profileAccount() !== input.accountId) return { ok: false, local: false, reason: "local_owner_required" };
  const credential = await readVscreenCredential(input.directory);
  const healthResponse = await transport(`http://127.0.0.1:${input.port}/health`, { signal: AbortSignal.timeout(2000) });
  const health = healthSchema.parse(await healthResponse.json());
  if (credential.profile !== input.profile || credential.backend.replace(/\/$/, "") !== input.backend || health.profile !== input.profile || health.server_url.replace(/\/$/, "") !== input.backend || health.pid !== credential.pid || health.daemon_id !== credential.daemon_id || !input.isCurrent() || await input.profileAccount() !== input.accountId) return { ok: false, local: false, reason: "local_owner_required" };
  if (input.runtimeId && !health.workspaces.some((w) => w.id === input.workspaceId && w.runtimes.includes(input.runtimeId!))) return { ok: false, local: false, reason: "runtime_not_local" };
  const response = await transport(`http://127.0.0.1:${input.port}/vscreen/desktop`, {
    method: "POST", redirect: "error", signal: AbortSignal.timeout(15000),
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${credential.capability}`, "X-Multica-Profile": input.profile, "X-Vscreen-Incarnation": credential.incarnation },
    body: JSON.stringify({ ...input.body, workspace_id: input.workspaceId, runtime_id: input.runtimeId }),
  });
  if (!input.isCurrent()) return { ok: false, local: false, reason: "local_owner_required" };
  const parsed = resultSchema.safeParse(await response.json());
  if (!parsed.success) return { ok: false, local: true, reason: "handoff_failed" };
  return { ok: response.ok && parsed.data.ok, local: parsed.data.local, reason: ["report_pending", "runtime_not_local", "local_owner_required", "capture_update_failed"].includes(parsed.data.reason ?? "") ? parsed.data.reason : response.ok ? undefined : "handoff_failed" };
}
