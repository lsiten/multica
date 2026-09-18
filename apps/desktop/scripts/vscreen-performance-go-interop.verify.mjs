// Invoked only by the opt-in Go test; the producer uses injected file-backed media.
import { randomBytes } from "node:crypto";
import { machineIdentity } from "./vscreen-remote-identity.mjs";
import { remoteRoute } from "./vscreen-remote-route.mjs";
import assert from "node:assert/strict";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { launchPerformanceChromium, startPerformanceViewer } from "./vscreen-performance-browser.mjs";
import { evaluatePerformance } from "./vscreen-performance-metrics.mjs";
import { evaluateRemoteNetwork } from "./vscreen-remote-network.mjs";
if(process.env.VSCREEN_RUN_HEADLESS_SMOKE!=="1")throw new Error("headless_interop_opt_in_required");
let input="";for await(const bytes of process.stdin){input+=bytes;if(input.length>8192)throw new Error("config_capacity");}
const config=JSON.parse(input),browser=await launchPerformanceChromium(),contexts=[],peers=[];
const rpc=async(path,body)=>{const response=await fetch(config.baseURL+path,{method:body?"POST":"GET",headers:{Authorization:`Bearer ${config.nonce}`,"Content-Type":"application/json"},body:body?JSON.stringify(body):undefined,signal:AbortSignal.timeout(10000)});assert.equal(response.status,200);return response.json();};
const poll=async(fn)=>{const end=Date.now()+10000;for(;;){const result=await fn();if(result)return result;if(Date.now()>end)throw Error("contract_observation_timeout");await new Promise(resolve=>setTimeout(resolve,50));}};
const normalize=value=>value.replaceAll(":","").toLowerCase();
const match=(native,sample)=>{const browser=sample.network,np=native.selected_pair,bp=browser.selected_pair;assert.equal(native.available,true);assert.equal(native.source_id,sample.sourceID);assert.ok(native.grant_id);assert.equal(native.viewer_id,browser.viewer_id);assert.equal(np.state,"succeeded");assert.equal(bp.state,"succeeded");assert.equal(np.nominated,true);assert.equal(bp.nominated,true);assert.deepEqual(np.local,bp.remote);assert.deepEqual(np.remote,bp.local);assert.equal(native.dtls.available,true);assert.equal(native.dtls.algorithm,"sha-256");assert.equal(native.dtls.local_fingerprint,normalize(browser.dtls.remote.fingerprint));assert.equal(native.dtls.remote_fingerprint,normalize(browser.dtls.local.fingerprint));assert.notEqual(native.route.kind,"lan");};
const observed=[],phases=[];let clean=false,lastClosed;
try{
 for(const mode of ["actual-supported","controlled-false","controlled-missing"]){
  const expected=mode==="actual-supported"?{width:1600,height:900}:{width:1280,height:720};
  for(const id of ["performance-0","performance-1"]){
   const context=await browser.newContext();contexts.push(context);
   if(mode!=="actual-supported")await context.addInitScript((kind)=>Object.defineProperty(navigator,"mediaCapabilities",{configurable:true,value:kind==="controlled-missing"?undefined:{decodingInfo:async()=>({supported:false,smooth:false,powerEfficient:false})}}),mode);
   peers.push(await startPerformanceViewer(await context.newPage(),{baseURL:config.baseURL,nonce:config.nonce,viewerID:id,source:{source_id:"one"}}));
  }
  const phase={mode,observations:[]};phases.push(phase);
  const capture=async()=>poll(async()=>{
   const samples=await Promise.all(peers.map(p=>p.sample())),metrics=await rpc("/metrics");
   if(samples.some(s=>s.decodedFrames<3||!s.network?.dtls?.remote||s.network.selected_pair.state!=="succeeded")||metrics.network.viewers.some(v=>!v.available||v.selected_pair?.state!=="succeeded"))return null;
   assert.equal(metrics.network.run_id,samples[0].clockSamples[0].clockEpoch);
   for(const s of samples){
    assert.equal(s.failure,null);assert.equal(s.negotiated.width,expected.width);assert.equal(s.negotiated.height,expected.height);assert.deepEqual(s.actualDecodedSize,expected);
    const proof=s.receiveOfferEvidence;assert.equal(proof.declarationApplied,mode==="actual-supported");
    if(mode!=="controlled-missing"){assert.ok(proof.queries.length>0);for(const q of proof.queries){assert.deepEqual({...q.configuration.video,contentType:undefined},{contentType:undefined,width:1600,height:900,bitrate:20000000,framerate:30});assert.equal(q.configuration.type,"webrtc");assert.equal(q.supported,mode==="actual-supported");assert.equal(q.smooth,mode==="actual-supported");}}
    else {assert.equal(proof.queryAvailable,false);assert.deepEqual(proof.queries,[]);}
    for(const field of ["localCodecOnly","answerCodecOnly"]){assert.equal(proof[field].some(line=>line.includes("max-recv-level=")),mode==="actual-supported");assert.ok(proof[field].every(line=>!/ice-|candidate:|fingerprint:/.test(line)));}
    match(metrics.network.viewers.find(v=>v.viewer_id===s.viewerID),s);s.network.route=await remoteRoute(s.network.selected_pair);
   }
   const observation={network:metrics.network,browsers:samples.map(s=>({viewerID:s.viewerID,sourceID:s.sourceID,decodedFrames:s.decodedFrames,negotiated:s.negotiated,actualDecodedSize:s.actualDecodedSize,receiveOfferEvidence:s.receiveOfferEvidence,network:s.network}))};observed.push(observation);phase.observations.push(observation);return observation;
  });
  const first=await capture();
  if(mode==="actual-supported"){
   await poll(async()=>{const value=await capture();return value.network.viewers.every((v,i)=>v.selected_pair.bytes_sent>first.network.viewers[i].selected_pair.bytes_sent)&&value.browsers.every((v,i)=>v.decodedFrames>first.browsers[i].decodedFrames)?value:null;});
   const grant=first.network.viewers[0].grant_id;await peers[0].switchSource({source_id:"two"},"switch");const switched=await capture();assert.equal(switched.network.viewers[0].source_id,"two");assert.notEqual(switched.network.viewers[0].grant_id,grant);
  }else{
   const verdict=evaluatePerformance({viewers:first.browsers});assert.equal(verdict.status,"blocked");assert.ok(verdict.failed.includes("negotiated_quality_below_target"));phase.strict900Gate="blocked";
  }
  for(const peer of peers)await peer.close();peers.length=0;lastClosed=(await rpc("/metrics")).network;assert.deepEqual(lastClosed.viewers,[]);
  for(const context of contexts)await context.close();contexts.length=0;
 }
 clean=true;
 const challenge=randomBytes(32).toString("hex"),identity=await machineIdentity(challenge);
 const verdict=evaluateRemoteNetwork({kind:"same-host-private-session",run_id:challenge,host_identity:identity,remote_identity:identity,observations:observed.map((o,i)=>({elapsed_ms:i+1,clock_epoch:o.network.run_id,producer:o.network,browsers:o.browsers.map(b=>b.network)}))},phases[0].observations[0].browsers.map(b=>({viewerID:b.viewerID,sourceID:b.sourceID})),0);
 assert.equal(verdict.verified,false);assert.ok(verdict.reasons.includes("independent_machine_identity_unverified"));assert.ok(verdict.reasons.includes("lan_physical_route_unverified"));
 await writeFile(join(config.evidence,"interop-observed.json"),JSON.stringify({scope:"same-host-file-fixture-only",phases,observations:observed,closed_network:lastClosed,lan_verdict:verdict,native_capture:false,formal_performance:false},null,2)+"\n",{mode:0o600});
}finally{for(const peer of peers){try{await peer.close();}catch{clean=false;}}for(const context of contexts)await context.close();await browser.close();}
console.log(JSON.stringify({passed:true,cleanup_confirmed:clean,scope:"synthetic-local-Go-Pion-Chromium-contract"}));
