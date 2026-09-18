// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { createHash } from "node:crypto";
import { chmod, mkdir, mkdtemp, readFile, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runDesktopNativeSmoke } from "./native-smoke";
const roots:string[]=[];
afterEach(async()=>{await Promise.all(roots.splice(0).map((p)=>rm(p,{recursive:true,force:true})));});
async function fixture(scenario="diagnostics") {
 const root=await realpath(await mkdtemp(join(tmpdir(),"desktop-smoke-")));roots.push(root);
 const bundle=join(root,"Multica.app"),directory=join(root,"invocation");await mkdir(directory,{mode:0o700});await mkdir(join(bundle,"Contents/MacOS"),{recursive:true});await mkdir(join(bundle,"Contents/Resources/app.asar.unpacked/resources/bin"),{recursive:true});
 const executable=join(bundle,"Contents/MacOS/Multica"),helper=join(bundle,"Contents/Resources/app.asar.unpacked/resources/bin/multica");await writeFile(executable,"fake main never executed");await writeFile(helper,"fake helper never executed");
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
describe("packaged Desktop native smoke isolation",()=>{
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
 it.each(["valid", "duplicate", "unclean", "foreign"])("checks performance NDJSON and private ready identity: %s",async(kind)=>{
  const f=await fixture("performance");
  await writeFile(join(f.directory,"performance-ready.json"),JSON.stringify({type:"performance-ready",schema_version:1,executable:kind==="foreign"?"/foreign":f.helper,version:"v1",commit:"abc123",nonce:"c".repeat(64)}),{mode:0o600});
  const original=f.run.getMockImplementation()!;
  f.run.mockImplementation(async(file,args)=>{
    const result=await original(file,args);if(args[0]!=="internal-vscreen-smoke")return result;
    const final=JSON.stringify({type:"performance-result",schema_version:1,cleanup_confirmed:kind!=="unclean",errors:[]});
    return {...result,stdout:JSON.stringify({type:"performance-ready"})+"\n"+final+(kind==="duplicate"?"\n"+final:"")};
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
