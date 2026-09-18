import { startRemoteViewers } from "./vscreen-remote-transport.mjs";
import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { mkdtemp, lstat, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { startPerformanceViewer, correlateRenderedBatch, launchPerformanceChromium } from "./vscreen-performance-browser.mjs";
import { evaluatePerformance, PERFORMANCE_REQUIREMENTS } from "./vscreen-performance-metrics.mjs";

const delay=(ms,signal)=>new Promise((resolve,reject)=>{if(signal?.aborted){reject(signal.reason??new Error("aborted"));return;}const abort=()=>{clearTimeout(timer);reject(signal.reason??new Error("aborted"));};const timer=setTimeout(()=>{signal?.removeEventListener("abort",abort);resolve();},ms);signal?.addEventListener("abort",abort,{once:true});});
export async function readPerformanceReady(directory, signal, exitState=()=>false) {
  const deadline=Date.now()+30_000, path=join(directory,"performance-ready.json");
  while(Date.now()<deadline){
    try{const info=await lstat(path);if(!info.isFile()||info.mode%512!==0o600||info.size>65536)throw new Error("performance_ready_not_private");const ready=JSON.parse(await readFile(path,"utf8"));const endpoint=new URL(ready.base_url);if(ready.type!=="performance-ready"||ready.schema_version!==1||endpoint.protocol!=="http:"||endpoint.hostname!=="127.0.0.1"||typeof ready.nonce!=="string"||ready.nonce.length<32||ready.host_clock!=="mach_continuous_time_ns"||ready.sources?.length!==2)throw new Error("invalid_performance_ready");return ready;}catch(error){if(error.code!=="ENOENT")throw error;}
    if(exitState())throw new Error("performance_helper_exited_before_ready");await delay(50,signal);
  }
  throw new Error("performance_ready_timeout");
}
async function rpc(ready,path,body,timeoutMs=15_000){const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),timeoutMs);try{const response=await fetch(ready.base_url+path,{method:body===undefined?"GET":"POST",headers:{Authorization:`Bearer ${ready.nonce}`,...(body===undefined?{}:{"Content-Type":"application/json"})},body:body===undefined?undefined:JSON.stringify(body),signal:controller.signal});if(!response.ok)throw new Error(`performance_rpc_${response.status}`);return await response.json();}finally{clearTimeout(timeout);}}
function normalizeResources(raw){const normalized={};for(const name of ["native","go"]){const process=raw?.[name];normalized[name]={availability:process?.availability,samples:process?.samples?.map((s)=>({elapsedMs:s.elapsed_ms,rssBytes:s.rss_bytes,cpuTimeNs:s.cpu_time_ns,fdCount:s.fd_count,activeCallbacks:s.active_callbacks,activeEncoders:s.active_encoders,bytesSentTotal:s.bytes_sent_total}))};}return normalized;}
function normalizeGPU(raw){const convert=(s)=>({elapsedMs:s.elapsed_ms,deviceUtilizationPercent:s.device_utilization_percent,rendererUtilizationPercent:s.renderer_utilization_percent,tilerUtilizationPercent:s.tiler_utilization_percent,inUseSystemMemoryBytes:s.in_use_system_memory_bytes});return raw?{...raw,baseline:raw.baseline?.map(convert),samples:raw.samples?.map(convert)}:undefined;}

// Shared by direct-bundle and Desktop-launched smoke. It owns only fresh,
// account-free browser contexts and never starts a native process itself.
export async function drivePerformanceSession(ready,options,dependencies={}){
  let browser=null, remote=null;
  const duration=options.durationMs??ready.duration_ms??PERFORMANCE_REQUIREMENTS.durationMs, cycles=options.cycles??ready.cycles??PERFORMANCE_REQUIREMENTS.cycles;
  const evidence={schemaVersion:1,mode:options.mode??ready.mode??"acceptance",durationMs:0,workload:"dynamic-owned-fixture",requested:{width:1600,height:900,fps:30},networkScope:"loopback",networkEvidence:{kind:"same-host-private-session"},provenance:{native:"selected-bundle",renderer:"chromium-rvfc-canvas",synthetic:dependencies.fixture===true},policy:{memory:"fixed-statistical-steady-state-v1",clockMaxDriftPPM:100},viewers:[],cycles:[],cleanupConfirmed:false,limitations:["Local loopback is not LAN acceptance.","Latency ends at a frame actually observed after Chromium rVFC/canvas sampling, not physical scanout.","System GPU samples are not attributable to these processes."]};
  let final=null, failure=null;const peers=[];const contexts=[];
  try{
    if(options.remoteViewerConfig) {
      remote=await (dependencies.startRemoteViewers??startRemoteViewers)(ready,options,(path,body)=>rpc(ready,path,body),dependencies);
      peers.push(...remote.peers);evidence.networkScope="remote-lan-candidate";evidence.networkEvidence=remote.evidence;evidence.limitations=evidence.limitations.filter((value)=>!value.startsWith("Local loopback"));
      for(let index=0;index<2;index++)evidence.viewers.push({viewerID:`performance-${index}`,sourceID:ready.sources[0].source_id,latencyUpperBoundsMs:[],maxClockUncertaintyMs:0,latencyMethod:"fixture-draw-to-browser-observed-upper-bound",switchFirstDecodedMs:[]});
    }else{
    browser=await launchPerformanceChromium(dependencies.chromium);
    for(let index=0;index<2;index++){
      const context=await browser.newContext({viewport:{width:1700,height:1000}});contexts.push(context);
      const peer=await startPerformanceViewer(await context.newPage(),{baseURL:ready.base_url,nonce:ready.nonce,viewerID:`performance-${index}`,source:ready.sources[0]});peers.push(peer);
      evidence.viewers.push({viewerID:`performance-${index}`,sourceID:ready.sources[0].source_id,latencyUpperBoundsMs:[],maxClockUncertaintyMs:0,latencyMethod:"fixture-draw-to-browser-observed-upper-bound",switchFirstDecodedMs:[]});
    }
    }
    if(remote){
      const deadline=performance.now()+20000;let connected=false;
      while(performance.now()<deadline){
        const batches=await Promise.all(peers.map((peer)=>peer.sample()));
        if(batches.some((batch)=>batch.failure))throw new Error("remote_viewer_start_failed");
        if(batches.every((batch)=>batch.renderedFrames>0 && batch.network?.selected_pair?.state==="succeeded" && batch.network?.dtls?.state==="connected")){connected=true;break;}
        await delay(100,options.signal);
      }
      if(!connected)throw new Error("remote_media_connection_unconfirmed");
    }
    const started=performance.now();let lastResources=null;
    for(;;){
      const networkBatches=[];
      for(const [index,peer]of peers.entries()){
        const batch=await peer.sample(),frames=correlateRenderedBatch(batch,100),viewer=evidence.viewers[index];
        if(remote)networkBatches.push({network:batch.network,clockEpoch:batch.clockSamples.at(-1)?.clockEpoch});
        Object.assign(viewer,{negotiated:batch.negotiated,observedDurationMs:batch.observedDurationMs,renderedFrames:batch.renderedFrames,uniqueDynamicFrames:batch.uniqueDynamicFrames,decodedFrames:batch.decodedFrames,invalidMarkers:batch.invalidMarkers,staleSourceFrames:batch.staleSourceFrames});
        for(const frame of frames){viewer.latencyUpperBoundsMs.push(frame.upperMs);viewer.maxClockUncertaintyMs=Math.max(viewer.maxClockUncertaintyMs,frame.clockUncertaintyMs);}
        await rpc(ready,"/samples",{schema_version:1,phase:"steady",viewer_id:viewer.viewerID,source_id:viewer.sourceID,browser_elapsed_ms:performance.now()-started,frames:frames.map((f)=>({frame_id:f.frameID,source_tag:f.sourceTag,fixture_draw_host_ns:f.fixtureDrawHostNs,browser_observed_ms:f.browserObservedMs,latency_upper_ms:f.upperMs,clock_uncertainty_ms:f.clockUncertaintyMs})),counters:{rendered_frames:batch.renderedFrames,decoded_frames:batch.decodedFrames,unique_dynamic_frames:batch.uniqueDynamicFrames,invalid_markers:batch.invalidMarkers,stale_source_frames:batch.staleSourceFrames,bytes_received:batch.bytesReceived}});
      }
      lastResources=await rpc(ready,"/metrics");
      if(remote){if(evidence.networkEvidence.observations.length>=4000)throw new Error("network_sample_capacity");if(new Set(networkBatches.map((b)=>b.clockEpoch)).size!==1)throw new Error("remote_clock_epoch_mismatch");evidence.networkEvidence.observations.push({elapsed_ms:performance.now()-started,producer:lastResources.network,clock_epoch:networkBatches[0]?.clockEpoch,browsers:networkBatches.map((b)=>b.network)});}
      if(evidence.viewers.every((v)=>v.observedDurationMs>=duration))break;
      if(performance.now()-started>duration+45_000)throw new Error("rendered_duration_not_reached");
      await delay(1000,options.signal);
    }
    evidence.durationMs=Math.min(...evidence.viewers.map((v)=>v.observedDurationMs));
    // Two runtime/source rendering is an explicit probe, not inferred from two peers.
    await peers[0].sample();await peers[1].switchSource(ready.sources[1],"concurrent");
    const probeDeadline=performance.now()+15_000;let concurrent=false;
    while(performance.now()<probeDeadline){const a=await peers[0].sample(),b=await peers[1].sample();if(a.failure||b.failure)throw new Error(a.failure??b.failure);if(a.frames.some((f)=>f.sourceTag===ready.sources[0].source_tag)&&b.frames.some((f)=>f.sourceTag===ready.sources[1].source_tag)){concurrent=true;break;}await delay(50,options.signal);}
    evidence.concurrentProbe={runtimeIDs:ready.sources.map((s)=>s.runtime_id),sourceIDs:ready.sources.map((s)=>s.source_id),rendered:concurrent};
    for(let turn=0;turn<(evidence.mode==="acceptance"?30:2);turn++)for(const [index,peer]of peers.entries()){
      const target=ready.sources[(turn+index)%2];await peer.switchSource(target,"switch");
      const deadline=performance.now()+5000;let value=null, switchedBatch=null;
      while(performance.now()<deadline){const batch=await peer.sample();if(batch.failure)throw new Error(batch.failure);if(batch.frames.some((f)=>f.sourceTag===target.source_tag)){value=batch.switchFirstDecodedMs.at(-1);switchedBatch=batch;break;}await delay(20,options.signal);}
      if(!Number.isFinite(value))throw new Error("first_decoded_switch_unavailable");evidence.viewers[index].switchFirstDecodedMs.push(value);
      if(remote){const network=(await rpc(ready,"/metrics")).network;const native=network?.viewers?.find((v)=>v.viewer_id===evidence.viewers[index].viewerID);evidence.networkEvidence.switch_observations.push({viewer_id:evidence.viewers[index].viewerID,source_id:target.source_id,source_tag:target.source_tag,frame_source_tag:switchedBatch.frames.find((f)=>f.sourceTag===target.source_tag)?.sourceTag,clock_epoch:switchedBatch.clockSamples.at(-1)?.clockEpoch,producer_epoch:network?.run_id,native,browser:switchedBatch.network});}
      await rpc(ready,"/samples",{schema_version:1,phase:"switch",viewer_id:evidence.viewers[index].viewerID,source_id:target.source_id,browser_elapsed_ms:performance.now()-started,frames:[],counters:{},switch_first_decoded_ms:value});
    }
    for(const peer of peers)await peer.close();peers.length=0;
    if(remote){await remote.close();evidence.networkEvidence.cleanup_confirmed=true;remote=null;}
    const lifecycle=await rpc(ready,"/cycles",{count:cycles},600_000);if(lifecycle.errors?.length)throw new Error("native_lifecycle_cycle_failed");evidence.cycles=(lifecycle.cycles??[]).map((c)=>({cycle:c.cycle,captureOpened:c.capture_opened,encodedFrameReceived:c.encoded_frame_received,disposed:c.disposed,fixtureExited:c.fixture_exited,managedDisplaysAfter:c.managed_displays_after,activeCallbacksAfter:c.active_callbacks_after,activeEncodersAfter:c.active_encoders_after,fdDelta:c.fd_delta,measurementsAvailable:c.measurements_available}));
    final=await rpc(ready,"/finish",{},30_000);
    if(final.errors?.length)failure="native_measurement_failed";
    evidence.resources=normalizeResources(lastResources.resources);evidence.sharedSource={captureSessions:lastResources.shared_source?.capture_sessions,encoderSessions:lastResources.shared_source?.encoder_sessions,independentlyObserved:lastResources.shared_source?.independently_observed};evidence.systemGPU=normalizeGPU(lastResources.system_gpu);evidence.cleanupConfirmed=final.cleanup_confirmed===true;
  }catch(error){failure=error.message;}finally{
    for(const peer of peers){try{await peer.close();}catch{failure??="viewer_cleanup_unconfirmed";}}
    for(const context of contexts){try{await context.close();}catch{failure??="browser_context_cleanup_unconfirmed";}}
    if(remote){try{await remote.close();evidence.networkEvidence.cleanup_confirmed=true;}catch{failure??="remote_cleanup_unconfirmed";}}
    if(browser){try{await browser.close();}catch{failure??="browser_cleanup_unconfirmed";}}
    if(!final){try{final=await rpc(ready,"/finish",{},30_000);evidence.cleanupConfirmed=final.cleanup_confirmed===true;}catch{failure??="native_cleanup_unconfirmed";}}
  }
  if(failure)evidence.failure=failure;
  const assessment=evaluatePerformance(evidence);if(failure){assessment.status="blocked";assessment.failed.push(failure);}
  await writeFile(join(options.evidence,"performance-evidence.json"),JSON.stringify({evidence,nativeResult:final,assessment},null,2)+"\n");
  return {evidence,assessment,nativeResult:final};
}

export async function runBundlePerformance(options,dependencies={}){
  if(!options.allowGui)throw Object.assign(new Error("GUI smoke requires explicit opt-in"),{code:"gui_not_authorized"});
  const directory=await mkdtemp(join(options.evidence,"performance-private-"));
  const nonce=randomBytes(32).toString("hex");const config={schema_version:1,nonce,parent_pid:process.pid,evidence_dir:directory,mode:options.mode??"acceptance",duration_ms:options.durationMs??1_800_000,cycles:options.cycles??30,requested:{width:1600,height:900,fps:30}};
  await writeFile(join(directory,"performance-config.json"),JSON.stringify(config),{mode:0o600});
  const launch=dependencies.spawn??spawn;const child=launch(options.helper,["internal-vscreen-smoke","performance",directory],{env:{...process.env,MULTICA_RUN_VSCREEN_GUI_SMOKE:"1"},stdio:["ignore","pipe","pipe"]});
  let exited=false,exitCode=null;
  child.stdout.on("data",()=>{});child.stderr.on("data",()=>{});
  const exit=new Promise((resolve)=>{child.once("error",()=>{exited=true;resolve();});child.once("exit",(code)=>{exited=true;exitCode=code;resolve();});});
  try{
    const ready=await readPerformanceReady(directory,options.signal,()=>exited);
    if(ready.executable!==options.helper||ready.version!==options.version||ready.commit!==options.commit||ready.nonce!==nonce)throw new Error("performance_helper_identity_mismatch");
    const result=await drivePerformanceSession(ready,{...options,evidence:options.evidence},dependencies);
    await Promise.race([exit,delay(10000).then(()=>{throw new Error("performance_helper_exit_unconfirmed");})]);
    if(exitCode!==0){result.evidence.failure="performance_helper_failed";result.assessment=evaluatePerformance(result.evidence);await writeFile(join(options.evidence,"performance-evidence.json"),JSON.stringify(result,null,2)+"\n");}
    return result;
  }finally{
    if(!exited){child.kill("SIGTERM");await Promise.race([exit,delay(10000)]);if(!exited)child.kill("SIGKILL");}
    // Process termination is never promoted to successful native cleanup.
  }
}
