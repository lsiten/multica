// @vitest-environment node
import { expect, it } from "vitest";
import { mkdtemp, mkdir, readFile, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";
import { prepareVscreenReceiveOffer } from "../../../packages/core/runtimes/vscreen-receive-offer.mjs";
import { startPerformanceViewer } from "./vscreen-performance-browser.mjs";
import { remoteWorkerHash, REMOTE_WORKER_FILES } from "./vscreen-remote-identity.mjs";

it("injects the exact shared receive helper into the private viewer page",async()=>{
 let received;
 const page={route:async()=>{},unroute:async()=>{},goto:async()=>{},evaluate:async(_fn,config)=>{received=config;}};
 await startPerformanceViewer(page,{baseURL:"http://127.0.0.1:1234",nonce:"owned",viewerID:"fixture"});
 expect(received.receiveOfferSource).toBe(prepareVscreenReceiveOffer.toString());
 expect(received.receiveOfferSource).toContain("20000000");
});
it("remote fixed hash covers the same core helper bytes, including a changed copy",async()=>{
 const dir=await mkdtemp(join(tmpdir(),"owned-vscreen-worker-hash-"));
 try{
  const scripts=join(dir,"apps/desktop/scripts");await mkdir(scripts,{recursive:true});
  expect(REMOTE_WORKER_FILES).toContain("../../../packages/core/runtimes/vscreen-receive-offer.mjs");
  for(const name of REMOTE_WORKER_FILES){const path=join(scripts,name);await mkdir(dirname(path),{recursive:true});await writeFile(path,await readFile(new URL(name,import.meta.url)));}
  const copy=await import(pathToFileURL(join(scripts,"vscreen-remote-identity.mjs")).href);expect(await copy.remoteWorkerHash()).toBe(await remoteWorkerHash());
  const helper=join(dir,"packages/core/runtimes/vscreen-receive-offer.mjs");await writeFile(helper,(await readFile(helper,"utf8"))+"\n");expect(await copy.remoteWorkerHash()).not.toBe(await remoteWorkerHash());
 }finally{await rm(dir,{recursive:true,force:true});}
});
