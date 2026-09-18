// @vitest-environment node
import { afterEach, expect, it } from "vitest";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, realpath, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { launchDesktopNativeSmoke } from "./vscreen-desktop-smoke-launcher.mjs";
const directories=[];afterEach(async()=>{await Promise.all(directories.splice(0).map((d)=>rm(d,{recursive:true,force:true})));});
async function fixture(){
 const root=await realpath(await mkdtemp(join(tmpdir(),"desktop-launcher-")));directories.push(root);const app=join(root,"Multica.app");
 await mkdir(join(app,"Contents/MacOS"),{recursive:true});await mkdir(join(app,"Contents/Resources/app.asar.unpacked/resources/bin"),{recursive:true});
 await writeFile(join(app,"Contents/Resources/app.asar"),fakeArchive());
 await writeFile(join(app,"Contents/Info.plist"),"fake plist");await writeFile(join(app,"Contents/MacOS/Multica"),"fake main");const helper=join(app,"Contents/Resources/app.asar.unpacked/resources/bin/multica");await writeFile(helper,"fake helper");
 const hash=(raw)=>createHash("sha256").update(raw).digest("hex");
 const options={app,evidence:join(root,"evidence"),scenario:"diagnostics",expectedHelper:{version:"v1",commit:"abc123",sha256:hash("fake helper")}};
 const calls=[];
 const deps={platform:"darwin",bundleInfo:async()=>({CFBundleIdentifier:"ai.multica.desktop",CFBundleExecutable:"Multica"}),launch:async(file,args,env)=>{
  calls.push({file,args,env});const raw=await readFile(env.MULTICA_DESKTOP_NATIVE_SMOKE_FILE,"utf8");const config=JSON.parse(raw);const directory=join(env.MULTICA_DESKTOP_NATIVE_SMOKE_FILE,"..");
  expect((await stat(env.MULTICA_DESKTOP_NATIVE_SMOKE_FILE)).mode&0o777).toBe(0o600);expect(args.join(" ")).not.toContain(config.nonce);expect(env.NODE_OPTIONS).toBeUndefined();
  await writeFile(join(directory,"desktop-native-smoke.json"),JSON.stringify({status:"passed",invocation_sha256:hash(raw),parent:{pid:777,parent_pid:process.pid,executable:file,sha256:config.mainSHA256,entry_sha256:config.entrySHA256},helper:{executable:helper,sha256:config.helper.sha256},scenario:config.scenario,cleanup_confirmed:true,desktop_launch_verified:true,tcc_attribution_verified:false}));
  return {pid:777,code:0,closed:true,forced:false,aborted:false};
 }};return {options,deps,calls};
}
it("launches the selected App directly with private invocation and isolated userData",async()=>{const f=await fixture();const result=await launchDesktopNativeSmoke(f.options,f.deps);expect(result).toMatchObject({status:"passed",desktop_launch_verified:true,tcc_attribution_verified:false});expect(f.calls[0].file).toBe(join(f.options.app,"Contents/MacOS/Multica"));expect(f.calls[0].args[0]).toContain("--user-data-dir=");expect(f.calls[0].env.MULTICA_RUN_VSCREEN_GUI_SMOKE).toBeUndefined();});
it("rejects GUI without authorization before process launch",async()=>{const f=await fixture();await expect(launchDesktopNativeSmoke({...f.options,scenario:"input"},f.deps)).rejects.toThrow("gui_not_authorized");expect(f.calls).toHaveLength(0);});
it("does not treat a forced App exit as successful cleanup",async()=>{const f=await fixture();const launch=f.deps.launch;f.deps.launch=async(...args)=>({...await launch(...args),forced:true});expect(await launchDesktopNativeSmoke(f.options,f.deps)).toMatchObject({status:"blocked",cleanup_confirmed:false});});
it("runs performance ready control concurrently and requires its measurement verdict",async()=>{
 const f=await fixture();let ready;const gate=new Promise((r)=>{ready=r;});const launch=f.deps.launch;f.deps.launch=async(...args)=>{await gate;return launch(...args);};
 const result=await launchDesktopNativeSmoke({...f.options,scenario:"performance",allowGui:true,timeoutMs:2700000,onReady:async({directory,signal})=>{expect(directory).toContain("desktop-invocation-");expect(signal.aborted).toBe(false);ready();return {status:"passed",frames:42};}},f.deps);
 expect(result).toMatchObject({status:"passed",measurements:{status:"passed",frames:42},tcc_attribution_verified:false});
});
it("rejects secret-shaped ready callback output",async()=>{const f=await fixture();expect(await launchDesktopNativeSmoke({...f.options,scenario:"performance",allowGui:true,onReady:async()=>({status:"passed",nonce:"private"})},f.deps)).toMatchObject({status:"blocked"});});

function fakeArchive(bootstrap="MULTICA_DESKTOP_NATIVE_SMOKE_FILE normal-startup.js unsafe_invocation_directory") {
 const entries={"package.json":JSON.stringify({main:"./out/main/index.js"}),"out/main/index.js":bootstrap,"out/main/normal-startup.js":"test-owned normal startup"};
 const tree={files:{}};const bodies=[];let offset=0;
 for(const [path,value] of Object.entries(entries)){const bytes=Buffer.from(value);let node=tree;const parts=path.split("/");for(const part of parts.slice(0,-1)){node.files[part]??={files:{}};node=node.files[part];}node.files[parts.at(-1)]={size:bytes.length,offset:String(offset)};bodies.push(bytes);offset+=bytes.length;}
 const json=Buffer.from(JSON.stringify(tree));const size=8+Math.ceil(json.length/4)*4;const header=Buffer.alloc(size);header.writeUInt32LE(size-4,0);header.writeUInt32LE(json.length,4);json.copy(header,8);const prefix=Buffer.alloc(8);prefix.writeUInt32LE(4,0);prefix.writeUInt32LE(size,4);return Buffer.concat([prefix,header,...bodies]);
}
it("refuses an old product bootstrap before launching the App",async()=>{const f=await fixture();await writeFile(join(f.options.app,"Contents/Resources/app.asar"),fakeArchive("requestSingleInstanceLock setupDaemonManager"));await expect(launchDesktopNativeSmoke(f.options,f.deps)).rejects.toThrow("unsupported_smoke_bootstrap");expect(f.calls).toHaveLength(0);});
it.each([false,true])("carries only explicit qualification interactive=%s into private invocation",async(interactive)=>{
 const f=await fixture();const launch=f.deps.launch;let invocation;
 f.deps.launch=async(file,args,env)=>{invocation=JSON.parse(await readFile(env.MULTICA_DESKTOP_NATIVE_SMOKE_FILE,"utf8"));return launch(file,args,env);};
 expect(await launchDesktopNativeSmoke({...f.options,scenario:"input-qualification",allowGui:true,interactive},f.deps)).toMatchObject({status:"passed"});
 expect(invocation).toMatchObject({scenario:"input-qualification",interactive});
});
