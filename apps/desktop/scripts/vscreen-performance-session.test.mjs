// @vitest-environment node
import { createServer } from "node:http";
import { mkdtemp, rm, writeFile, chmod } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, it } from "vitest";
import { drivePerformanceSession, readPerformanceReady } from "./vscreen-performance-session.mjs";
const dirs=[];afterEach(async()=>{await Promise.all(dirs.splice(0).map((d)=>rm(d,{recursive:true,force:true})));});
async function directory(){const d=await mkdtemp(join(tmpdir(),"vscreen-perf-test-"));dirs.push(d);return d;}
it("validates the private ready file without publishing its nonce",async()=>{const d=await directory();const path=join(d,"performance-ready.json");const ready={type:"performance-ready",schema_version:1,base_url:"http://127.0.0.1:1234",nonce:"a".repeat(64),host_clock:"mach_continuous_time_ns",sources:[{},{}]};await writeFile(path,JSON.stringify(ready),{mode:0o600});expect((await readPerformanceReady(d)).nonce).toBe(ready.nonce);await chmod(path,0o644);await expect(readPerformanceReady(d)).rejects.toThrow("not_private");});
it("renderer launch failure still requests native cleanup and cannot pass",async()=>{
 let finish=0;const server=createServer((req,res)=>{if(req.url==="/finish"&&req.headers.authorization===`Bearer ${"a".repeat(64)}`){finish++;res.setHeader("content-type","application/json");res.end(JSON.stringify({type:"performance-result",cleanup_confirmed:true}));}else{res.writeHead(404);res.end();}});
 await new Promise((resolve)=>server.listen(0,"127.0.0.1",resolve));
 try{const result=await drivePerformanceSession({base_url:`http://127.0.0.1:${server.address().port}`,nonce:"a".repeat(64),sources:[{},{}]},{evidence:await directory(),durationMs:1000,mode:"debug"},{fixture:true,chromium:{launch:async()=>{throw new Error("owned_renderer_unavailable");}}});expect(finish).toBe(1);expect(result.assessment.status).toBe("blocked");expect(result.assessment.failed).toContain("owned_renderer_unavailable");expect(result.evidence.cleanupConfirmed).toBe(true);}finally{await new Promise((resolve)=>server.close(resolve));}
});
