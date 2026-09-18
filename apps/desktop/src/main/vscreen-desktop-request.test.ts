// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { chmod, mkdir, mkdtemp, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { requestVscreenDesktop, readVscreenCredential } from "./vscreen-desktop-request";

const roots: string[] = [];
afterEach(async () => { await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))); });
async function fixture() {
  const directory = await realpath(await mkdtemp(join(tmpdir(), "vscreen-local-"))); roots.push(directory);
  await mkdir(join(directory,".desktop-vscreen"), { mode: 0o700 });
  const credential = { capability: "a".repeat(64), incarnation: "b".repeat(32), pid: 123, profile: "desktop-fixture", daemon_id: "daemon", backend: "https://fixture.invalid", started_at: "2026-09-18T00:00:00Z" };
  await writeFile(join(directory,".desktop-vscreen/credential.json"), JSON.stringify(credential), { mode: 0o600 });
  const health = { status: "running", pid: 123, profile: credential.profile, daemon_id: "daemon", server_url: credential.backend, launched_by: "desktop", os: "darwin", workspaces: [{ id: "workspace", runtimes: ["runtime"] }] };
  const transport = vi.fn<typeof fetch>().mockImplementation(async (url) => Response.json(String(url).endsWith("/health") ? health : { ok: true, local: true }));
  const input = { directory, port: 20111, profile: credential.profile, backend: credential.backend, accountId: "owner", workspaceId: "workspace", runtimeId: "runtime", body: { action: "takeover", intervention_id: "intervention", destination_source_id: "display:physical" }, isCurrent: () => true, profileAccount: async () => "owner", transport };
  return { credential, health, transport, input };
}
describe("trusted local handoff request", () => {
  it("authenticates an explicit transfer and keeps the capability in main-only headers", async () => {
    const f = await fixture(); expect(await requestVscreenDesktop(f.input)).toEqual({ ok: true, local: true, reason: undefined });
    const [url, init] = f.transport.mock.calls[1]!; expect(url).toBe("http://127.0.0.1:20111/vscreen/desktop");
    expect(init?.headers).toMatchObject({ Authorization: `Bearer ${f.credential.capability}`, "X-Vscreen-Incarnation": f.credential.incarnation });
    expect(JSON.parse(String(init?.body))).toMatchObject({ action: "takeover", workspace_id: "workspace", runtime_id: "runtime" });
    expect(String(init?.body)).not.toContain(f.credential.capability);
  });
  it.each(["account", "backend", "pid", "runtime", "generation"])("rejects stale or foreign %s before physical request", async (kind) => {
    const f = await fixture();
    if (kind === "account") f.input.profileAccount = async () => "other";
    if (kind === "backend") f.health.server_url = "https://other.invalid";
    if (kind === "pid") f.health.pid++;
    if (kind === "runtime") f.input.runtimeId = "foreign";
    if (kind === "generation") f.input.isCurrent = () => f.transport.mock.calls.length === 0;
    expect((await requestVscreenDesktop(f.input)).ok).toBe(false);
    expect(f.transport.mock.calls.some(([url]) => String(url).endsWith("/vscreen/desktop"))).toBe(false);
  });
  it("preserves report_pending without retrying physical moves", async () => {
    const f = await fixture(); f.transport.mockImplementation(async (url) => Response.json(String(url).endsWith("/health") ? f.health : { ok: false, local: true, reason: "report_pending" }, { status: String(url).endsWith("/health") ? 200 : 409 }));
    expect((await requestVscreenDesktop(f.input)).reason).toBe("report_pending"); expect(f.transport).toHaveBeenCalledTimes(2);
  });
  it.each(["mode", "symlink", "oversize"])("rejects unsafe credential %s", async (kind) => {
    const f = await fixture(); const file = join(f.input.directory,".desktop-vscreen/credential.json");
    if (kind === "mode") await chmod(file,0o644);
    if (kind === "oversize") await writeFile(file,"x".repeat(4097));
    if (kind === "symlink") { await rm(file); await symlink("elsewhere",file); }
    await expect(readVscreenCredential(f.input.directory)).rejects.toThrow(); expect(f.transport).not.toHaveBeenCalled();
  });
});
