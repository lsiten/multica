// Run after electron-vite build. This executes the compiled entry in a VM, never Electron.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { EventEmitter } from "node:events";
import { readFile, mkdir, mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { PassThrough } from "node:stream";
import { runInNewContext } from "node:vm";
const require = createRequire(import.meta.url);
const compiled = await readFile(new URL("../out/main/index.js", import.meta.url), "utf8");
const root = await realpath(await mkdtemp(join(tmpdir(), "desktop-built-entry-")));
const hash = (bytes) => createHash("sha256").update(bytes).digest("hex");
try {
  const bundle = join(root, "Multica.app"), directory = join(root, "private");
  const executable = join(bundle, "Contents/MacOS/Multica");
  const helper = join(bundle, "Contents/Resources/app.asar.unpacked/resources/bin/multica");
  const entryPath = join(bundle, "Contents/Resources/app.asar/out/main/index.js");
  for (const path of [executable, helper, entryPath]) await mkdir(dirname(path), { recursive: true });
  await mkdir(directory, { mode: 0o700 });
  await writeFile(executable, "owned fake main"); await writeFile(helper, "owned fake helper"); await writeFile(entryPath, compiled);
  const invocation = join(directory, "invocation.json");
  await writeFile(invocation, JSON.stringify({ schema: 1, nonce: "a".repeat(64), parentPID: 321, app: bundle, mainSHA256: hash("owned fake main"), entrySHA256: hash(compiled), helper: {version: "v1", commit: "abc123", sha256: hash("owned fake helper")}, scenario: "diagnostics", allowGui: false, expiresAt: Date.now()+60000, timeoutMs: 1000 }), { mode: 0o600 });
  let normalLoads = 0, readyCalls = 0; const paths = new Map(), spawned = [];
  let resolveExit;
  const exited = new Promise((resolve) => { resolveExit = resolve; });
  const app = { isPackaged: true, setPath: (name, path) => paths.set(name, path), setAppLogsPath() {}, commandLine: {appendSwitch() {}}, whenReady: async () => { readyCalls++; assert.equal(paths.get("userData"), join(directory, "user-data")); }, exit: resolveExit };
  class ParentPipe extends EventEmitter { resume() {} destroy() {} }
  const spawn = (file, args, options) => {
    spawned.push({ file, args }); assert.equal(options.env.HOME, undefined);
    const child = new EventEmitter(); child.pid=777; child.stdout=new PassThrough(); child.stderr=new PassThrough(); child.kill=()=>assert.fail("mock child must not be killed");
    queueMicrotask(() => {
      let output="";
      if (file==="/usr/bin/plutil") output=JSON.stringify({CFBundleIdentifier:"ai.multica.desktop",CFBundleExecutable:"Multica"});
      else if (file==="/usr/bin/codesign") output="";
      else if (file===helper && args[0]==="--version") output="multica v1 (commit: abc123, built: mock)";
      else if (file===helper && args[0]==="internal-vscreen-diagnostics") output=JSON.stringify({version:"v1",commit:"abc123",permissions:{accessibility:false,screen_recording:false}});
      else assert.fail("unexpected spawned command");
      child.stdout.end(output); child.emit("close",0);
    });
    return child;
  };
  const fakeProcess = Object.assign(new EventEmitter(), { platform:"darwin",arch:"arm64",execPath:executable,pid:456,ppid:321,getuid:()=>process.getuid(),env:{} });
  const sandbox = { __filename:entryPath, __dirname:dirname(entryPath), exports:{}, process:fakeProcess, Buffer, AbortController, setTimeout, clearTimeout, console,
    require: (name) => {
      if (name==="electron") return {app};
      if (name==="node:module") return {createRequire:(path)=>{assert.equal(path,entryPath);return (target)=>{assert.equal(target,"./normal-startup.js");normalLoads++;};}};
      if (name==="node:child_process") return {spawn};
      if (name==="node:net") return {Socket:ParentPipe};
      if (/^\.\/chunks\/[^/]+\.js$/.test(name)) {
        const output = {};
        runInNewContext(readFileSync(new URL(`../out/main/${name.slice(2)}`, import.meta.url), "utf8"), { ...sandbox, exports: output });
        return output;
      }
      assert.ok(name.startsWith("node:"), `unexpected compiled dependency ${name}`);
      return require(name);
    }
  };
  runInNewContext(compiled,{...sandbox},{filename:entryPath});
  assert.equal(normalLoads,1); assert.equal(readyCalls,0); assert.equal(spawned.length,0);
  normalLoads=0; fakeProcess.env.MULTICA_DESKTOP_NATIVE_SMOKE_FILE=invocation;
  runInNewContext(compiled,{...sandbox},{filename:entryPath});
  assert.equal(normalLoads,0);
  let timeout; const code=await Promise.race([exited,new Promise((_,reject)=>{timeout=setTimeout(()=>reject(new Error("compiled_entry_timeout")),5000);})]);clearTimeout(timeout);
  assert.equal(code,0); assert.equal(readyCalls,1); assert.equal(normalLoads,0); assert.equal(spawned.length,5);
  const report=JSON.parse(await readFile(join(directory,"desktop-native-smoke.json"),"utf8"));
  assert.equal(report.parent.entry_sha256,hash(compiled));assert.equal(report.helper.child_pid,777);assert.equal(report.desktop_launch_verified,true);assert.equal(report.tcc_attribution_verified,false);
  console.log(JSON.stringify({scenario:"compiled-thin-entry-with-real-smoke-implementation",status:"passed",entry_sha256:hash(compiled),normal_loaded_synchronously:true,smoke_product_loads:normalLoads,owned_mock_commands:spawned.length,gui_processes_launched:0}));
} finally { await rm(root,{recursive:true,force:true}); }
