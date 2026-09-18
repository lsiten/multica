// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { chmod, mkdir, mkdtemp, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { requestVscreenDesktop, readVscreenCredential } from "./vscreen-desktop-request";

// Authenticated handoff fixtures need actual POSIX private-file semantics.
const posixSecurity = process.platform !== "win32" && typeof process.getuid === "function";
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
describe.runIf(posixSecurity)("trusted local handoff request (POSIX credentials)", () => {
  it("authenticates an explicit transfer and keeps the capability in main-only headers", async () => {
    const f = await fixture(); expect(await requestVscreenDesktop(f.input)).toEqual({ ok: true, local: true, reason: undefined });
    const [url, init] = f.transport.mock.calls[1]!; expect(url).toBe("http://127.0.0.1:20111/vscreen/desktop");
    expect(init?.headers).toMatchObject({ Authorization: `Bearer ${f.credential.capability}`, "X-Vscreen-Incarnation": f.credential.incarnation });
    expect(JSON.parse(String(init?.body))).toMatchObject({ action: "takeover", workspace_id: "workspace", runtime_id: "runtime" });
    expect(String(init?.body)).not.toContain(f.credential.capability);
  });
  it.each(["backend", "pid", "runtime", "generation"])("rejects stale or foreign %s before physical request", async (kind) => {
    const f = await fixture();
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

it.runIf(posixSecurity)("returns bounded candidates only for an explicit list operation and never in status cache",async()=>{
 const f=await fixture();const candidates={windows:[{handle:"opaque",bundle_id:"org.example.Editor",title:"Private document"}],truncated:false};
 f.transport.mockImplementation(async(url)=>Response.json(String(url).endsWith("/health")?f.health:{ok:true,local:true,intervention_id:"i",selection_required:true,candidates}));
 const listed=await requestVscreenDesktop({...f.input,body:{action:"list_windows",intervention_id:"i"}});
 expect(listed.candidates?.windows[0]).toEqual({handle:"opaque",bundleId:"org.example.Editor",title:"Private document"});
 const status=await requestVscreenDesktop({...f.input,body:{action:"status"}});expect(status.candidates).toBeUndefined();expect(status.selectionRequired).toBe(true);
 f.transport.mockImplementation(async(url)=>Response.json(String(url).endsWith("/health")?f.health:{ok:true,local:true,candidates:{windows:[{...candidates.windows[0],pid:123}],truncated:false}}));
 expect((await requestVscreenDesktop({...f.input,body:{action:"list_windows"}})).ok).toBe(false);
});

it.runIf(posixSecurity)("bounds private candidate response bytes and drops metadata after a late context switch",async()=>{
 const f=await fixture();f.transport.mockImplementation(async(url)=>String(url).endsWith("/health")?Response.json(f.health):new Response("x".repeat(65*1024)));
 expect((await requestVscreenDesktop({...f.input,body:{action:"list_windows"}})).ok).toBe(false);
 let current=true;
 f.transport.mockImplementation(async(url)=>String(url).endsWith("/health")?Response.json(f.health):new Response(new ReadableStream({async pull(controller){await new Promise((resolve)=>setTimeout(resolve,0));current=false;controller.enqueue(new TextEncoder().encode(JSON.stringify({ok:true,local:true,candidates:{windows:[{handle:"opaque",bundle_id:"org.example.Editor",title:"Private document"}],truncated:false}})));controller.close();}})));
 const result=await requestVscreenDesktop({...f.input,isCurrent:()=>current,body:{action:"list_windows"}});expect(result.ok).toBe(false);expect(result.candidates).toBeUndefined();
});

it.each(["account","generation"])("rejects foreign %s before opening a credential on every platform",async(kind)=>{
 const f=await fixture();if(kind==="account")f.input.profileAccount=async()=>"other";else f.input.isCurrent=()=>false;
 expect(await requestVscreenDesktop(f.input)).toEqual({ok:false,local:false,reason:"local_owner_required"});expect(f.transport).not.toHaveBeenCalled();
});
it("refuses local handoff when POSIX owner identity is unavailable",async()=>{
 const f=await fixture(),descriptor=Object.getOwnPropertyDescriptor(process,"getuid");
 try{Object.defineProperty(process,"getuid",{configurable:true,value:undefined});await expect(requestVscreenDesktop(f.input)).rejects.toThrow("local_owner_required");expect(f.transport).not.toHaveBeenCalled();}
 finally{if(descriptor)Object.defineProperty(process,"getuid",descriptor);else delete (process as {getuid?:()=>number}).getuid;}
});
