// @vitest-environment node
import { describe, expect, it } from "vitest";
import { clockBounds, frameLatencyBound, decodeFrameMarker, memoryTrend, evaluatePerformance } from "./vscreen-performance-metrics.mjs";
import { correlateRenderedBatch } from "./vscreen-performance-browser.mjs";

function markerImage({source=7,frame=123,time=10000000000000001n}={}){
 const bytes=new Uint8Array(20),view=new DataView(bytes.buffer);bytes[0]=0x56;bytes[1]=0x53;view.setUint32(2,source);view.setUint32(6,frame);view.setBigUint64(10,time);
 let crc=0xffff;for(const value of bytes.subarray(0,18)){crc^=value<<8;for(let bit=0;bit<8;bit++)crc=(crc&0x8000)?((crc<<1)^0x1021)&0xffff:(crc<<1)&0xffff;}view.setUint16(18,crc);
 const width=60,height=24,pixels=new Uint8ClampedArray(width*height*4);
 for(let bit=0;bit<160;bit++){const white=(bytes[Math.floor(bit/8)]>>(7-bit%8))&1;const x=(bit%20)*3,y=Math.floor(bit/20)*3;for(let dy=0;dy<3;dy++)for(let dx=0;dx<3;dx++){const at=((y+dy)*width+x+dx)*4;pixels[at]=pixels[at+1]=pixels[at+2]=white?255:0;pixels[at+3]=255;}}
 return {pixels,width,height,layout:{x:0,y:0,cellSize:3,columns:20}};
}
function metrics(){
 const samples=Array.from({length:1801},(_,i)=>({elapsedMs:i*1000,rssBytes:20_000_000,cpuTimeNs:String(i*1000000),fdCount:20,activeCallbacks:1,activeEncoders:1,bytesSentTotal:i*1000}));
 const process={availability:Object.fromEntries(["rss","cpu","fd","callbacks","encoder","bandwidth"].map((key)=>[key,{available:true,method:"owned fixture"}])),samples};
 const gpuSample=(elapsedMs)=>({elapsedMs,deviceUtilizationPercent:10,rendererUtilizationPercent:5,tilerUtilizationPercent:5,inUseSystemMemoryBytes:1000});
 return {schemaVersion:1,mode:"acceptance",provenance:{native:"selected-bundle",renderer:"chromium-rvfc-canvas",synthetic:false},networkScope:"loopback",networkEvidence:{kind:"same-host-private-session"},durationMs:1800000,workload:"dynamic-owned-fixture",requested:{width:1600,height:900,fps:30},viewers:[0,1].map((i)=>({viewerID:`viewer-${i}`,sourceID:"source",negotiated:{width:1600,height:900,fps:30},observedDurationMs:1800000,renderedFrames:54000,uniqueDynamicFrames:54000,decodedFrames:54000,staleSourceFrames:0,invalidMarkers:0,latencyUpperBoundsMs:Array(45000).fill(100),latencyMethod:"fixture-draw-to-browser-observed-upper-bound",maxClockUncertaintyMs:2,switchFirstDecodedMs:Array(30).fill(500)})),sharedSource:{captureSessions:1,encoderSessions:1,independentlyObserved:true},concurrentProbe:{runtimeIDs:["a","b"],sourceIDs:["a","b"],rendered:true},resources:{native:structuredClone(process),go:structuredClone(process)},systemGPU:{scope:"system",attribution:"not_benchmark_specific",method:"IOKit fixture",availability:{available:true},baseline:[0,1000,2000].map(gpuSample),samples:Array.from({length:181},(_,i)=>gpuSample(i*10000))},cycles:Array.from({length:30},(_,i)=>({cycle:i,captureOpened:true,encodedFrameReceived:true,disposed:true,fixtureExited:true,managedDisplaysAfter:0,activeCallbacksAfter:0,activeEncodersAfter:0,fdDelta:0,measurementsAvailable:true})),cleanupConfirmed:true};
}

describe("performance evidence fidelity",()=>{
 it("bounds asymmetric clock exchange and preserves uint64 marker precision",()=>{
  const bound=clockBounds({browserSendMs:100,browserReceiveMs:110,hostReceiveNs:"1005000000",hostSendNs:"1007000000"});
  const latency=frameLatencyBound({browserObservedMs:120,fixtureDrawHostNs:"990000000"},bound,0);expect(latency.lowerMs).toBe(27);expect(latency.upperMs).toBe(35);expect(latency.clockUncertaintyMs).toBe(8);expect(latency.physicalScanoutVerified).toBe(false);
  const image=markerImage();expect(decodeFrameMarker(image.pixels,image.width,image.height,image.layout)).toEqual({sourceTag:7,frameID:123,fixtureDrawHostNs:"10000000000000001"});
 });
 it("rejects malformed markers rather than pairing data-channel PTS",()=>{
  const image=markerImage();image.pixels.fill(0,4*3*3,4*3*3+12);expect(()=>decodeFrameMarker(image.pixels,image.width,image.height,{...image.layout,x:100})).toThrow("outside");
  const corrupt=markerImage();for(let y=3;y<6;y++)for(let x=0;x<3;x++){const at=(y*corrupt.width+x)*4;corrupt.pixels[at]=255-corrupt.pixels[at];corrupt.pixels[at+1]=255-corrupt.pixels[at+1];corrupt.pixels[at+2]=255-corrupt.pixels[at+2];}expect(()=>decodeFrameMarker(corrupt.pixels,corrupt.width,corrupt.height,corrupt.layout)).toThrow("crc");
 });
 it("rejects missing, stale, stepped or inconsistent clock proof",()=>{
  expect(()=>clockBounds({browserSendMs:20,browserReceiveMs:10,hostReceiveNs:"1",hostSendNs:"2"})).toThrow();
  const bound=clockBounds({browserSendMs:0,browserReceiveMs:1,hostReceiveNs:"1000000",hostSendNs:"1000000"});expect(()=>frameLatencyBound({browserObservedMs:40000,fixtureDrawHostNs:"2"},bound,100)).toThrow("stale");
  expect(()=>correlateRenderedBatch({frames:[],clockSamples:[{clockEpoch:"a"},{clockEpoch:"b"}]})).toThrow("epoch");
  expect(()=>correlateRenderedBatch({frames:[],clockSamples:[{clockEpoch:"a",browserSendMs:0,browserReceiveMs:1,hostReceiveNs:"1000000",hostSendNs:"1000000"},{clockEpoch:"a",browserSendMs:100,browserReceiveMs:101,hostReceiveNs:"1000000000",hostSendNs:"1000000000"}]})).toThrow("discontinuity");
 });
 it("distinguishes GC/RSS noise from sustained growth without last-byte comparison",()=>{
  const flat=metrics().resources.native.samples;expect(memoryTrend(flat).status).toBe("no-detectable-sustained-growth");
  const noisy=flat.map((s,i)=>({...s,rssBytes:s.rssBytes+(i%60<30?1:-1)*100000}));expect(memoryTrend(noisy).status).toBe("no-detectable-sustained-growth");
  expect(memoryTrend(flat.map((s,i)=>({...s,rssBytes:s.rssBytes+i*10000}))).status).toBe("sustained-growth");
  expect(()=>memoryTrend(flat.filter((_,i)=>i%60===0))).toThrow("coverage");
  const lastNoise=structuredClone(flat);lastNoise.at(-1).rssBytes+=1000000;expect(memoryTrend(lastNoise).status).toBe("no-detectable-sustained-growth");
  const plateau=flat.map((s,i)=>({...s,rssBytes:s.rssBytes+(i>900?1000000:0)}));expect(memoryTrend(plateau).status).toBe("inconclusive");
 });
 it("can pass measured loopback scope but never promotes it to LAN or all",()=>{
  const assessment=evaluatePerformance(metrics());expect(assessment.localMeasurement.status).toBe("passed");expect(assessment.status).toBe("blocked");expect(assessment.planCoverage.lanAcceptance).toBe("unverified");expect(assessment.gpuScope).toBe("system-not-attributable-to-benchmark");
 });
 it.each([
  ["browser cleanup",(e)=>{e.failure="browser_cleanup_unconfirmed";}],
  ["debug",(e)=>{e.mode="debug";}], ["short",(e)=>{e.durationMs=1000;}], ["encoder only",(e)=>{e.viewers[0].renderedFrames=0;}], ["duplicate frames",(e)=>{e.viewers[0].uniqueDynamicFrames=1;}], ["quality fallback",(e)=>{e.viewers[0].negotiated.width=1280;}], ["latency",(e)=>{e.viewers[0].latencyUpperBoundsMs.fill(300);}], ["few cycles",(e)=>{e.cycles.pop();}], ["FD leak",(e)=>{e.cycles[0].fdDelta=1;}], ["unknown callbacks",(e)=>{e.resources.native.availability.callbacks={available:false,reason:"unavailable"};}], ["missing values",(e)=>{delete e.resources.go.samples[1].cpuTimeNs;}], ["unknown GPU",(e)=>{e.systemGPU.availability.available=false;}], ["fake GPU attribution",(e)=>{e.systemGPU.scope="process";}], ["not independently shared",(e)=>{e.sharedSource.independentlyObserved=false;}], ["cleanup failure",(e)=>{e.cleanupConfirmed=false;}],
 ])("blocks %s",(_name,mutate)=>{const evidence=metrics();mutate(evidence);expect(evaluatePerformance(evidence).localMeasurement.status).toBe("blocked");});
});

it("execution failure cannot be hidden by the independent LAN blocker",()=>{const evidence=metrics();expect(evaluatePerformance(evidence).localMeasurement.status).toBe("passed");evidence.failure="viewer_cleanup_unconfirmed";const result=evaluatePerformance(evidence);expect(result.localMeasurement.status).toBe("blocked");expect(result.localMeasurement.failed).toContain("viewer_cleanup_unconfirmed");expect(result.failed).toContain("lan_acceptance_unverified");});
