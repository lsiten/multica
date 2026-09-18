import { spawn, execFile } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { createReadStream } from "node:fs";
import { mkdir, mkdtemp, open, readFile, realpath, stat, writeFile } from "node:fs/promises";
import { isAbsolute, join, relative, sep } from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);
const scenarios = ["diagnostics", "lifecycle", "source", "video", "input", "takeover", "performance", "input-qualification"];
async function hash(path) { const digest=createHash("sha256");for await (const chunk of createReadStream(path)) digest.update(chunk);return digest.digest("hex"); }
async function inside(app,path) { const actual=await realpath(path);const child=relative(app,actual);if(!child||child===".."||child.startsWith(`..${sep}`)||isAbsolute(child)||!(await stat(actual)).isFile())throw new Error("bundle_path_escape");return actual; }

/**
 * @typedef {Object} DesktopNativeSmokeOptions
 * @property {string} app Absolute selected .app path; never a renderer-supplied helper path.
 * @property {string} evidence Absolute artifact directory. A private invocation subdirectory is created.
 * @property {"diagnostics"|"lifecycle"|"source"|"video"|"input"|"takeover"|"performance"|"input-qualification"} scenario
 * @property {{version:string,commit:string,sha256:string}} expectedHelper Validated selected-bundle identity from the outer runner.
 * @property {boolean} [allowGui=false] Explicit user authorization; diagnostics are always nonprompt/read-only.
 * @property {boolean} [interactive=false] Qualification-only explicit local human typing phase.
 * @property {number} [timeoutMs=180000] Child operation budget, 1..180 seconds, or up to 2700 seconds for performance.
 * @property {(context:{directory:string,signal:AbortSignal})=>Promise<{status:"passed"|"blocked",[key:string]:unknown}>} [onReady] Performance-only private-file control hook; never return nonce/token/authorization/base_url fields.
 * @property {AbortSignal} [signal] Cancellation makes the result blocked even after a clean exit.
 */

/**
 * Start a NEW selected App main process, never `open -a` or an existing user instance.
 * Invocation capability stays in an owned 0600 file, not argv, renderer, or logs.
 * `desktop_launch_verified` proves this process chain only; TCC attribution remains unverified.
 * Dependencies are command/spawn seams for pure tests. Default behavior requires explicit invocation.
 * @param {DesktopNativeSmokeOptions} options
 * @param {{platform?:string, bundleInfo?:(path:string)=>Promise<object>, launch?:(file:string,args:string[],env:object,timeout:number,signal?:AbortSignal)=>Promise<object>}} [dependencies]
 */
export async function launchDesktopNativeSmoke(options, dependencies = {}) {
  if ((dependencies.platform ?? process.platform) !== "darwin") throw new Error("macos_required");
  if (!isAbsolute(options.app) || !options.app.endsWith(".app") || !isAbsolute(options.evidence) || !scenarios.includes(options.scenario)) throw new Error("invalid_desktop_smoke_options");
  if (options.interactive !== undefined && (typeof options.interactive !== "boolean" || (options.interactive && options.scenario !== "input-qualification"))) throw new Error("invalid_interactive_scenario");
  if (options.scenario !== "diagnostics" && options.allowGui !== true) throw new Error("gui_not_authorized");
  if (options.signal?.aborted) throw new Error("desktop_smoke_cancelled");
  const timeout = options.timeoutMs ?? (options.scenario === "performance" ? 2700000 : 180000);
  if (!Number.isInteger(timeout) || timeout < 1000 || timeout > (options.scenario === "performance" ? 2700000 : 180000) || !/^[a-f0-9]{64}$/.test(options.expectedHelper?.sha256 ?? "")) throw new Error("invalid_desktop_smoke_options");
  if (options.scenario === "performance" && typeof options.onReady !== "function") throw new Error("performance_ready_callback_required");
  if (options.onReady && options.scenario !== "performance") throw new Error("invalid_ready_callback");
  const app = await realpath(options.app);
  const plist = await inside(app,join(app,"Contents","Info.plist"));
  const info = await (dependencies.bundleInfo ?? (async (path) => JSON.parse((await exec("/usr/bin/plutil",["-convert","json","-o","-",path],{timeout:10000,maxBuffer:65536})).stdout)))(plist);
  if (info.CFBundleIdentifier !== "ai.multica.desktop" || typeof info.CFBundleExecutable !== "string" || !/^[^/\\]+$/.test(info.CFBundleExecutable)) throw new Error("bundle_identity_mismatch");
  const entry = await readPackagedSmokeEntry(app);
  const executable = await inside(app,join(app,"Contents","MacOS",info.CFBundleExecutable));
  const helper = await inside(app,join(app,"Contents","Resources","app.asar.unpacked","resources","bin","multica"));
  if (await hash(helper) !== options.expectedHelper.sha256) throw new Error("helper_identity_mismatch");
  await mkdir(options.evidence,{recursive:true});
  const evidence = await realpath(options.evidence);
  const directory = await mkdtemp(join(evidence,"desktop-invocation-"));
  const userData = join(directory,"user-data");await mkdir(userData,{mode:0o700});
  const config = {schema:1,nonce:randomBytes(32).toString("hex"),parentPID:process.pid,app,mainSHA256:await hash(executable),entrySHA256:entry.sha256,helper:options.expectedHelper,scenario:options.scenario,interactive:options.interactive===true,allowGui:options.allowGui===true,expiresAt:Date.now()+600000,timeoutMs:timeout};
  const raw = JSON.stringify(config);const invocation = join(directory,"invocation.json");await writeFile(invocation,raw,{mode:0o600,flag:"wx"});
  const env = {PATH:"/usr/bin:/bin:/usr/sbin:/sbin",LANG:"en_US.UTF-8",MULTICA_DESKTOP_NATIVE_SMOKE_FILE:invocation,...(options.allowGui ? {MULTICA_RUN_VSCREEN_GUI_SMOKE:"1"}: {})};
  const lifetime = new AbortController();
  const cancel = () => lifetime.abort(); options.signal?.addEventListener("abort",cancel,{once:true});
  let measurements, callbackFailed=false;
  if (options.signal?.aborted) { options.signal.removeEventListener("abort",cancel); throw new Error("desktop_smoke_cancelled"); }
  const childWork = (dependencies.launch ?? launchApp)(executable,[`--user-data-dir=${userData}`],env,timeout+75000,lifetime.signal);
  const controlWork = options.onReady ? Promise.resolve().then(()=>options.onReady({directory,signal:lifetime.signal})).then((result)=>{measurements=result;},()=>{callbackFailed=true;lifetime.abort();}) : Promise.resolve();
  const child = await childWork;
  lifetime.abort();
  let cleanupTimer;
  await Promise.race([controlWork,new Promise((resolve)=>{cleanupTimer=setTimeout(()=>{callbackFailed=true;resolve();},15000);})]);
  clearTimeout(cleanupTimer);options.signal?.removeEventListener("abort",cancel);
  if (measurements) {
    const encoded=JSON.stringify(measurements);
    if (encoded.length>256*1024 || /"(?:nonce|token|authorization|base_url)"\s*:/i.test(encoded)) {callbackFailed=true;measurements=undefined;}
  }
  const reportPath=join(directory,"desktop-native-smoke.json");
  let report;
  try { if((await stat(reportPath)).size>2*1024*1024)throw new Error("oversize");report=JSON.parse(await readFile(reportPath,"utf8")); } catch { return {status:"blocked",reportPath,error:"desktop_report_missing",cleanup_confirmed:false}; }
  const expectedHash=createHash("sha256").update(raw).digest("hex");
  const verified=report.invocation_sha256===expectedHash && report.parent?.pid===child.pid && report.parent?.parent_pid===process.pid && report.parent?.executable===executable && report.parent?.sha256===config.mainSHA256 && report.parent?.entry_sha256===entry.sha256 && report.helper?.executable===helper && report.helper?.sha256===options.expectedHelper.sha256 && report.scenario===options.scenario;
  if (!verified || callbackFailed || (options.scenario==="performance" && measurements?.status!=="passed") || child.code!==0 || !child.closed || child.forced || child.aborted || options.signal?.aborted || report.status!=="passed" || report.desktop_launch_verified!==true || report.tcc_attribution_verified!==false || report.cleanup_confirmed!==true) return {status:"blocked",reportPath,report,measurements,error:verified?"desktop_smoke_failed":"desktop_provenance_mismatch",cleanup_confirmed:false};
  return {status:"passed",reportPath,report,measurements,desktop_launch_verified:report.desktop_launch_verified===true,tcc_attribution_verified:false,cleanup_confirmed:true};
}

function launchApp(file,args,env,timeout,signal) {
  return new Promise((resolveResult) => {
    const child=spawn(file,args,{env,stdio:["ignore","ignore","ignore","pipe"]});
    let aborted=false,forced=false,settled=false,killTimer,reapTimer;
    const finish=(code,closed)=>{if(settled)return;settled=true;child.stdio[3]?.destroy();clearTimeout(timer);clearTimeout(killTimer);clearTimeout(reapTimer);signal?.removeEventListener("abort",stop);resolveResult({code,closed,pid:child.pid,forced,aborted});};
    const stop=()=>{if(aborted||settled)return;aborted=true;child.kill("SIGTERM");killTimer=setTimeout(()=>{if(!settled){forced=true;child.kill("SIGKILL");reapTimer=setTimeout(()=>finish(null,false),3000);}},30000);};
    const timer=setTimeout(stop,timeout);signal?.addEventListener("abort",stop,{once:true});
    child.once("error",()=>finish(null,false));child.once("close",(code)=>finish(code,true));if(signal?.aborted)stop();
  });
}

// ASAR uses an 8-byte size pickle, then a length-prefixed JSON header. Read only
// the known bootstrap entries; do not execute archived code or resolve archive links.
async function readPackagedSmokeEntry(app) {
  const archive=await inside(app,join(app,"Contents/Resources/app.asar"));const file=await open(archive,"r");
  try {
    const prefix=Buffer.alloc(8);if((await file.read(prefix,0,8,0)).bytesRead!==8 || prefix.readUInt32LE(0)!==4)throw new Error("unsupported_smoke_bootstrap");
    const headerSize=prefix.readUInt32LE(4);if(headerSize<8 || headerSize>32*1024*1024)throw new Error("unsupported_smoke_bootstrap");
    const header=Buffer.alloc(headerSize);if((await file.read(header,0,headerSize,8)).bytesRead!==headerSize || header.readUInt32LE(0)!==headerSize-4 || header.readUInt32LE(4)>headerSize-8)throw new Error("unsupported_smoke_bootstrap");
    const tree=JSON.parse(header.subarray(8,8+header.readUInt32LE(4)).toString("utf8"));const archiveSize=(await file.stat()).size;
    async function entry(path,limit) {
      let node=tree;for(const part of path.split("/")){node=node.files?.[part];if(!node || node.link)throw new Error("unsupported_smoke_bootstrap");}
      const offset=Number(node.offset);if(node.unpacked || !Number.isInteger(node.size) || node.size<=0 || node.size>limit || !Number.isSafeInteger(offset) || offset<0 || 8+headerSize+offset+node.size>archiveSize)throw new Error("unsupported_smoke_bootstrap");
      const bytes=Buffer.alloc(node.size);if((await file.read(bytes,0,bytes.length,8+headerSize+offset)).bytesRead!==bytes.length)throw new Error("unsupported_smoke_bootstrap");return bytes;
    }
    const pkg=JSON.parse((await entry("package.json",65536)).toString("utf8"));if(pkg.main!=="./out/main/index.js" && pkg.main!=="out/main/index.js")throw new Error("unsupported_smoke_bootstrap");
    const bytes=await entry("out/main/index.js",128*1024);const code=bytes.toString("utf8");
    if(!code.includes("MULTICA_DESKTOP_NATIVE_SMOKE_FILE") || !code.includes("normal-startup.js") || !code.includes("unsafe_invocation_directory") || /fixPath|setupDaemonManager|requestSingleInstanceLock/.test(code))throw new Error("unsupported_smoke_bootstrap");
    await entry("out/main/normal-startup.js",4*1024*1024);
    return {sha256:createHash("sha256").update(bytes).digest("hex")};
  } finally {await file.close();}
}
