// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { createHash } from "node:crypto";
import { chmod, mkdir, mkdtemp, readFile, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runDesktopNativeSmoke } from "./native-smoke";
// Packaged macOS fixtures exercise real POSIX ownership, modes and symlinks.
const posixSecurity = process.platform !== "win32" && typeof process.getuid === "function";
const roots:string[]=[];
afterEach(async()=>{await Promise.all(roots.splice(0).map((p)=>rm(p,{recursive:true,force:true})));});
async function fixture(scenario="diagnostics") {
 const root=await realpath(await mkdtemp(join(tmpdir(),"desktop-smoke-")));roots.push(root);
 const bundle=join(root,"Multica.app"),directory=join(root,"invocation");await mkdir(directory,{mode:0o700});await mkdir(join(bundle,"Contents/MacOS"),{recursive:true});await mkdir(join(bundle,"Contents/Resources/app.asar.unpacked/resources/MulticaDaemon.app/Contents/MacOS"),{recursive:true});
 const executable=join(bundle,"Contents/MacOS/Multica"),helper=join(bundle,"Contents/Resources/app.asar.unpacked/resources/MulticaDaemon.app/Contents/MacOS/multica");await writeFile(executable,"fake main never executed");await writeFile(helper,"fake helper never executed");await chmod(helper,0o755);
 const hash=(s:string)=>createHash("sha256").update(s).digest("hex");
 const entryPath=join(bundle,"Contents/Resources/app.asar/out/main/index.js");await mkdir(join(entryPath,".."),{recursive:true});await writeFile(entryPath,"fake bootstrap");
 const config={schema:1,entrySHA256:hash("fake bootstrap"),nonce:"a".repeat(64),parentPID:321,app:bundle,mainSHA256:hash("fake main never executed"),helper:{version:"v1",commit:"abc123",sha256:hash("fake helper never executed")},scenario,allowGui:scenario!=="diagnostics",expiresAt:Date.now()+60000,timeoutMs:1000};
 const path=join(directory,"invocation.json");await writeFile(path,JSON.stringify(config),{mode:0o600});
 const app={isPackaged:true,setName:vi.fn(),setPath:vi.fn(),setAppLogsPath:vi.fn(),commandLine:{appendSwitch:vi.fn()},whenReady:vi.fn(async()=>{})};
 const run=vi.fn(async(file:string,args:string[])=>{
  let stdout="";
  if(file.endsWith("plutil"))stdout=JSON.stringify({CFBundleIdentifier:"ai.multica.desktop",CFBundleExecutable:"Multica",CFBundleShortVersionString:"1"});
  else if(args[0]==="--version")stdout="multica v1 (commit: abc123, built: fixture)\n";
  else if(args[0]==="internal-vscreen-diagnostics")stdout=JSON.stringify({version:"v1",commit:"abc123",permissions:{accessibility:false,screen_recording:false}});
  else if(args[0]==="internal-vscreen-smoke")stdout=JSON.stringify({version:"v1",commit:"abc123",executable:helper,scenario,status:"passed",disposed:true,host_closed:true});
  return {code:0,pid:777,stdout,stderr:"",closed:true,forced:false,aborted:false};
 });
 const dependencies={entryPath,executable,platform:"darwin",pid:456,parentPID:321,uid:process.getuid!(),guiOptIn:scenario!=="diagnostics",run,watchParent:vi.fn(()=>vi.fn())};
 return {root,directory,path,config,app,dependencies,run,helper};
}
describe.runIf(posixSecurity)("packaged Desktop native smoke isolation (POSIX files)",()=>{
 it("isolates before ready and records actual launch chain without claiming TCC attribution",async()=>{
  const f=await fixture();expect(await runDesktopNativeSmoke(f.app,f.path,f.dependencies)).toBe(0);
  expect(f.app.setPath).toHaveBeenCalledWith("userData",join(f.directory,"user-data"));expect(f.app.setPath.mock.invocationCallOrder[0]).toBeLessThan(f.app.whenReady.mock.invocationCallOrder[0]!);
  const report=JSON.parse(await readFile(join(f.directory,"desktop-native-smoke.json"),"utf8"));expect(report).toMatchObject({status:"passed",desktop_launch_verified:true,tcc_attribution_verified:false,parent:{pid:456,parent_pid:321},helper:{executable:f.helper}});
  expect(JSON.stringify(report)).not.toContain(f.config.nonce);
  expect(f.run.mock.calls.some(([,args])=>args[0]==="internal-vscreen-smoke")).toBe(false);
 });
 it.each(["parent","hash","gui","replay","expired","directory_mode","entry","helper_escape"])("fails closed for %s without launching a helper",async(kind)=>{
  const f=await fixture("input");if(kind==="expired")f.config.expiresAt=Date.now()-1000;if(kind==="directory_mode")await chmod(f.directory,0o755);if(kind==="entry")f.dependencies.entryPath="";if(kind==="helper_escape"){await rm(f.helper);await writeFile(join(f.root,"foreign-helper"),"fake helper never executed");await symlink(join(f.root,"foreign-helper"),f.helper);}if(kind==="parent")f.dependencies.parentPID=99;if(kind==="hash")f.config.mainSHA256="b".repeat(64);if(kind==="gui")f.dependencies.guiOptIn=false;if(kind==="replay")await writeFile(join(f.directory,"consumed"),"used");await writeFile(f.path,JSON.stringify(f.config),{mode:0o600});
  expect(await runDesktopNativeSmoke(f.app,f.path,f.dependencies)).toBe(1);expect(f.run).not.toHaveBeenCalled();
 });
 it("records denied diagnostics instead of treating child parentage as a permission grant",async()=>{
  const f=await fixture();const original=f.run.getMockImplementation()!;f.run.mockImplementation(async(file,args)=>{const result=await original(file,args);return args[0]==="internal-vscreen-diagnostics"?{...result,code:1}:result;});
  expect(await runDesktopNativeSmoke(f.app,f.path,f.dependencies)).toBe(1);const report=JSON.parse(await readFile(join(f.directory,"desktop-native-smoke.json"),"utf8"));expect(report.native.permissions.accessibility).toBe(false);expect(report.desktop_launch_verified).toBe(true);expect(report.tcc_attribution_verified).toBe(false);
 });
 it.each(["valid", "duplicate", "unclean", "foreign", "secret", "missing-ready", "late-ready"])("checks child performance NDJSON after producer deletes private ready: %s",async(kind)=>{
  const f=await fixture("performance");
  await writeFile(join(f.directory,"performance-ready.json"),JSON.stringify({type:"performance-ready",schema_version:1,executable:kind==="foreign"?"/foreign":f.helper,version:"v1",commit:"abc123",nonce:"c".repeat(64)}),{mode:0o600});
  const original=f.run.getMockImplementation()!;
  f.run.mockImplementation(async(file,args)=>{
    const result=await original(file,args);if(args[0]!=="internal-vscreen-smoke")return result;
    const final=JSON.stringify({type:"performance-result",schema_version:1,cleanup_confirmed:kind!=="unclean",errors:[]});
    await rm(join(f.directory,"performance-ready.json"));
    const ready=JSON.stringify({type:"performance-ready",schema_version:1,executable:kind==="foreign"?"/foreign":f.helper,version:"v1",commit:"abc123",...(kind==="secret"?{nonce:"c".repeat(64)}:{})});
    return {...result,stdout:kind==="missing-ready"?final:kind==="late-ready"?final+"\n"+ready:ready+"\n"+final+(kind==="duplicate"?"\n"+final:"")};
  });
  expect(await runDesktopNativeSmoke(f.app,f.path,f.dependencies)).toBe(kind==="valid"?0:1);
  const report=JSON.parse(await readFile(join(f.directory,"desktop-native-smoke.json"),"utf8"));
  expect(report.cleanup_confirmed).toBe(kind==="valid");expect(JSON.stringify(report)).not.toContain("c".repeat(64));
 });
 it.each(["forced","aborted","unclosed"])("does not claim cleanup pass for %s child",async(kind)=>{
  const f=await fixture("input");const original=f.run.getMockImplementation()!;f.run.mockImplementation(async(file,args)=>{const result=await original(file,args);return args[0]==="internal-vscreen-smoke"?{...result,forced:kind==="forced",aborted:kind==="aborted",closed:kind!=="unclosed"}:result;});
  expect(await runDesktopNativeSmoke(f.app,f.path,f.dependencies)).toBe(1);const report=JSON.parse(await readFile(join(f.directory,"desktop-native-smoke.json"),"utf8"));expect(report.cleanup_confirmed).toBe(false);
 });
});

it.runIf(posixSecurity).each(["default", "interactive", "unconfirmed", "incomplete", "certified"])("routes and bounds qualification evidence: %s", async (kind) => {
 const interactive = ["interactive", "unconfirmed"].includes(kind);
 const passed = ["default", "interactive"].includes(kind);
 const f=await fixture("input-qualification");
 const config=JSON.parse(await readFile(f.path,"utf8"));config.interactive=interactive;await writeFile(f.path,JSON.stringify(config));
 const original=f.run.getMockImplementation()!;
 const qualification={scope:"experimental-same-bundle-disposable-fixture",effects_verified:true,completion_verified:true,production_certified:false,fixture_closed:true,control_revoked:true,old_lease_refused:true,foreground_continuity:interactive?"verified_manual_fixture_challenge":"unverified_requires_human_typing_phase",manual:{status:interactive?"verified_manual_fixture_challenge":"unverified",user_confirmed:interactive,scratch_closed:interactive},native:{version:"v1",commit:"abc123",executable:f.helper,scenario:"input-qualification",status:"passed",disposed:true,host_closed:true}};
 if(kind==="unconfirmed") qualification.manual.user_confirmed=false;
 if(kind==="incomplete") qualification.completion_verified=false;
 if(kind==="certified") qualification.production_certified=true;
 f.run.mockImplementation(async(file,args)=>args[0]==="internal-vscreen-input-qualification"?{code:0,pid:789,stdout:JSON.stringify(qualification),stderr:"",closed:true,forced:false,aborted:false}:original(file,args));
 expect(await runDesktopNativeSmoke(f.app,f.path,f.dependencies)).toBe(passed?0:1);
 expect(f.run.mock.calls.find(([,args])=>args[0]==="internal-vscreen-input-qualification")?.[1]).toEqual(["internal-vscreen-input-qualification",f.directory,...(interactive?["--interactive"]:[])]);
 const report=JSON.parse(await readFile(join(f.directory,"desktop-native-smoke.json"),"utf8"));expect(report.qualification).toEqual(qualification);if(kind!=="certified")expect(report.native).toEqual(qualification.native);expect(report.tcc_attribution_verified).toBe(false);
});

it.each(["win32","linux"])("rejects unsupported native smoke platform %s before readiness or helper launch",async(platform)=>{
 const app={isPackaged:true,setName:vi.fn(),setPath:vi.fn(),setAppLogsPath:vi.fn(),commandLine:{appendSwitch:vi.fn()},whenReady:vi.fn(async()=>{})};const run=vi.fn();
 expect(await runDesktopNativeSmoke(app,undefined,{platform,run})).toBe(1);expect(run).not.toHaveBeenCalled();expect(app.whenReady).not.toHaveBeenCalled();expect(app.setPath).not.toHaveBeenCalled();
});
