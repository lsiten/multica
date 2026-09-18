// @vitest-environment node
import { expect, it } from "vitest";
import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { fileURLToPath } from "node:url";
import { performance as nodeClock } from "node:perf_hooks";
import { writeFile } from "node:fs/promises";
import { loadPerformanceChromium } from "./vscreen-performance-browser.mjs";
import { installSyntheticSender } from "./vscreen-performance-headless.fixture.mjs";
import { remoteChannel } from "./vscreen-remote-protocol.mjs";
import { machineIdentity, remoteWorkerHash } from "./vscreen-remote-identity.mjs";
import { viewerSignalGate } from "./vscreen-remote-transport.mjs";
import { evaluateRemoteNetwork } from "./vscreen-remote-network.mjs";

it.skipIf(process.env.VSCREEN_RUN_HEADLESS_SMOKE!=="1")("actual local stdio worker relays H264 frames, switches and cleans up, but cannot prove independent LAN",async()=>{
 const browser=await loadPerformanceChromium().launch({headless:true}), context=await browser.newContext();
 const senders=new Map();for(const id of ["performance-0","performance-1"]){const page=await context.newPage();await page.goto("about:blank");await installSyntheticSender(page);senders.set(id,page);}
 const run=randomBytes(32).toString("hex"),hostIdentity=await machineIdentity(run),sources=[{source_id:"source-a",source_tag:1},{source_id:"source-b",source_tag:2}];
 const child=spawn(process.execPath,[fileURLToPath(new URL("./vscreen-remote-worker.mjs",import.meta.url)),"--stdio"],{stdio:["pipe","pipe","pipe"],env:{PATH:"/usr/bin:/bin",HOME:process.env.HOME}});
 let stderrBytes=0;child.stderr.on("data",(chunk)=>{stderrBytes+=chunk.length;});let code=null,closed=false;const exited=new Promise(resolve=>child.once("close",value=>{code=value;closed=true;resolve();}));
 const calls=[],gate=viewerSignalGate(run,sources,async(path,body)=>{
  calls.push(path);
  if(path==="/clock"){const now=String(BigInt(Math.round((nodeClock.timeOrigin+nodeClock.now())*1e6)));return{host_receive_ns:now,host_send_ns:now,clock_epoch:"synthetic-clock"};}
  const page=senders.get(body.viewer_id);
  if(path==="/offer"){const answer=await page.evaluate(({offer,source})=>window.ownedSyntheticSender.offer(offer,source),{offer:body.offer,source:body.source_id});return{answer,source_tag:body.source_id==="source-a"?1:2,marker:{x:8,y:8,cell_size:8,columns:20},negotiated:{width:640,height:360,fps:30}};}
  if(path==="/viewer/close")await page.evaluate(()=>window.ownedSyntheticSender.close());
  return{};
 });
 const channel=remoteChannel(child.stdout,child.stdin,{signal:gate.signal});let evidence;
 try{
  const hello=await channel.request("hello",{run_id:run,worker_hash:await remoteWorkerHash()});expect(hello.identity.machine_id_hash).toBe(hostIdentity.machine_id_hash);
  const started=await channel.request("start",{run_id:run,sources});expect(started.fresh_contexts).toBe(2);
  let samples;await expect.poll(async()=>{samples=await Promise.all(started.viewer_ids.map(viewer_id=>channel.request("sample",{run_id:run,viewer_id})));return Math.min(...samples.map(s=>s.observedDurationMs));},{timeout:15000}).toBeGreaterThanOrEqual(3000);
  for(const sample of samples){expect(sample.failure).toBeNull();expect(sample.renderedFrames).toBeGreaterThan(30);expect(sample.invalidMarkers).toBe(0);expect(sample.network.selected_pair.state).toBe("succeeded");expect(sample.network.dtls.state).toBe("connected");expect(sample.network.selected_pair.nominated).toBe(true);expect(sample.network.dtls.local.algorithm).toBe("sha-256");expect(sample.network.dtls.remote.algorithm).toBe("sha-256");}
  await channel.request("switch",{run_id:run,viewer_id:"performance-0",source_id:"source-b",phase:"switch"});let switched;
  await expect.poll(async()=>{switched=await channel.request("sample",{run_id:run,viewer_id:"performance-0"});return switched.frames.some(f=>f.sourceTag===2);},{timeout:10000}).toBe(true);
  expect((await channel.request("close",{run_id:run})).cleanup_confirmed).toBe(true);channel.close();child.stdin.end();
  await expect.poll(()=>closed,{timeout:5000}).toBe(true);expect(code).toBe(0);
  const network={kind:"controlled-ssh-worker",run_id:run,worker_hash:hello.worker_hash,local_worker_hash:hello.worker_hash,host_identity:hostIdentity,remote_identity:hello.identity,observations:[],cleanup_confirmed:true};
  const verdict=evaluateRemoteNetwork(network,[],3000);expect(verdict.verified).toBe(false);expect(verdict.reasons).toContain("independent_machine_identity_unverified");
  evidence={scope:"synthetic-local-worker-only-no-ssh-no-native",same_machine:true,cleanup_confirmed:true,worker_exit:code,browser_version:started.browser_version,viewers:samples.map(s=>({rendered:s.renderedFrames,decoded:s.decodedFrames,duration_ms:s.observedDurationMs,invalid_markers:s.invalidMarkers,ice_state:s.network.selected_pair.state,dtls_state:s.network.dtls.state,local_certificate:s.network.dtls.local,remote_certificate:s.network.dtls.remote})),switch_ms:switched.switchFirstDecodedMs,lan_verdict:verdict,calls,stderr_bytes:stderrBytes};
 }finally{channel.close();child.stdin.end();if(!closed){child.kill("SIGTERM");await Promise.race([exited,new Promise(resolve=>setTimeout(resolve,5000))]);if(!closed)child.kill("SIGKILL");}await context.close();await browser.close();}
 if(process.env.VSCREEN_REMOTE_SYNTHETIC_EVIDENCE)await writeFile(process.env.VSCREEN_REMOTE_SYNTHETIC_EVIDENCE,JSON.stringify(evidence,null,2)+"\n",{mode:0o600});
},45000);
