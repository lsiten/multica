// @vitest-environment node
import { createServer } from "node:http";
import { mkdtemp, rm, writeFile, chmod } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, it } from "vitest";
import { drivePerformanceSession, readPerformanceReady } from "./vscreen-performance-session.mjs";
// chmod/uid security cannot be represented by Windows file mode bits.
const posixSecurity = process.platform !== "win32" && typeof process.getuid === "function";
const dirs=[];afterEach(async()=>{await Promise.all(dirs.splice(0).map((d)=>rm(d,{recursive:true,force:true})));});
async function directory(){const d=await mkdtemp(join(tmpdir(),"vscreen-perf-test-"));dirs.push(d);return d;}
it.runIf(posixSecurity)("validates the private ready file without publishing its nonce",async()=>{const d=await directory();const path=join(d,"performance-ready.json");const ready={type:"performance-ready",schema_version:1,base_url:"http://127.0.0.1:1234",nonce:"a".repeat(64),host_clock:"mach_continuous_time_ns",sources:[{},{}]};await writeFile(path,JSON.stringify(ready),{mode:0o600});expect((await readPerformanceReady(d)).nonce).toBe(ready.nonce);await chmod(path,0o644);await expect(readPerformanceReady(d)).rejects.toThrow("not_private");});
it("renderer launch failure still requests native cleanup and cannot pass",async()=>{
 let finish=0;const server=createServer((req,res)=>{if(req.url==="/finish"&&req.headers.authorization===`Bearer ${"a".repeat(64)}`){finish++;res.setHeader("content-type","application/json");res.end(JSON.stringify({type:"performance-result",cleanup_confirmed:true}));}else{res.writeHead(404);res.end();}});
 await new Promise((resolve)=>server.listen(0,"127.0.0.1",resolve));
 try{const result=await drivePerformanceSession({base_url:`http://127.0.0.1:${server.address().port}`,nonce:"a".repeat(64),sources:[{},{}]},{evidence:await directory(),durationMs:1000,mode:"debug"},{fixture:true,chromium:{launch:async()=>{throw new Error("owned_renderer_unavailable");}}});expect(finish).toBe(1);expect(result.assessment.status).toBe("blocked");expect(result.assessment.failed).toContain("owned_renderer_unavailable");expect(result.evidence.cleanupConfirmed).toBe(true);}finally{await new Promise((resolve)=>server.close(resolve));}
});

it.each([false,true])("remote contexts close before native cycles; cleanup failure=%s cannot pass",async(failClose)=>{
 const calls=[],nonce="host-only-"+"a".repeat(64),sources=[{source_id:"a",source_tag:1,runtime_id:"r1"},{source_id:"b",source_tag:2,runtime_id:"r2"}];
 const server=createServer((req,res)=>{expect(req.headers.authorization).toBe(`Bearer ${nonce}`);calls.push(req.url);res.setHeader("Content-Type","application/json");res.end(JSON.stringify(req.url==="/finish"?{cleanup_confirmed:true,errors:[]}:req.url==="/cycles"?{cycles:[]}:{}));});await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));
 const peers=[0,1].map(i=>{let source=sources[0];return{sample:async()=>({viewerID:`performance-${i}`,sourceID:source.source_id,negotiated:{width:1600,height:900,fps:30},frames:[{frameID:1,sourceTag:source.source_tag,fixtureDrawHostNs:"1000000",browserObservedMs:2}],clockSamples:[{browserSendMs:0,browserReceiveMs:1,hostReceiveNs:"1000000",hostSendNs:"1000000",clockEpoch:"epoch"}],observedDurationMs:1000,renderedFrames:30,decodedFrames:30,uniqueDynamicFrames:30,invalidMarkers:0,staleSourceFrames:0,switchFirstDecodedMs:[1],network:{selected_pair:{state:"succeeded"},dtls:{state:"connected"}}}),switchSource:async(next)=>{source=next;},close:async()=>{}};});
 try{const result=await drivePerformanceSession({base_url:`http://127.0.0.1:${server.address().port}`,nonce,sources},{evidence:await directory(),durationMs:1000,cycles:0,mode:"debug",remoteViewerConfig:"/owned/config",allowRemoteViewer:true},{fixture:true,startRemoteViewers:async()=>({peers,evidence:{kind:"controlled-ssh-worker",observations:[],switch_observations:[]},close:async()=>{calls.push("remote-close");if(failClose)throw Error("remote_cleanup_unconfirmed");}})});
 expect(result.assessment.status).toBe("blocked");expect(calls).toContain("/finish");expect(JSON.stringify(result)).not.toContain(nonce);if(failClose){expect(calls).not.toContain("/cycles");expect(result.assessment.localMeasurement.status).toBe("blocked");}else{expect(calls.indexOf("remote-close")).toBeLessThan(calls.indexOf("/cycles"));expect(result.evidence.networkEvidence.cleanup_confirmed).toBe(true);}
 }finally{await new Promise(resolve=>server.close(resolve));}
});
it("refuses a ready file without private mode bits on every platform",async()=>{
 const d=await directory(),path=join(d,"performance-ready.json");await writeFile(path,"{}",{mode:0o666});await chmod(path,0o666);
 await expect(readPerformanceReady(d)).rejects.toThrow("performance_ready_not_private");
});
