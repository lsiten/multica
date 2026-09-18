import { evaluateRemoteNetwork } from "./vscreen-remote-network.mjs";
// Pure measurement math. Only the native/browser collectors may assert provenance.
export const PERFORMANCE_REQUIREMENTS = Object.freeze({ durationMs: 1_800_000, cycles: 30, viewers: 2, width: 1600, height: 900, fps: 30, minRenderedFps: 25, latencyP95Ms: 250, switchP95Ms: 2000, maxClockAgeMs: 30_000 });
const ns = (value) => { if (typeof value !== "string" || !/^\d+$/.test(value)) throw new Error("invalid_monotonic_ns"); return BigInt(value); };
const browserNS = (value) => { if (!Number.isFinite(value) || value < 0) throw new Error("invalid_browser_clock"); return BigInt(Math.round(value * 1e6)); };

// A two-way exchange bounds offset without assuming symmetric network latency.
export function clockBounds(sample) {
  const sent = browserNS(sample.browserSendMs), received = browserNS(sample.browserReceiveMs);
  const hostReceived = ns(sample.hostReceiveNs), hostSent = ns(sample.hostSendNs);
  if (received < sent || hostSent < hostReceived || hostSent-hostReceived > received-sent) throw new Error("inconsistent_clock_exchange");
  return { lowerOffsetNs: (hostSent-received).toString(), upperOffsetNs: (hostReceived-sent).toString(), observedAtMs: sample.browserReceiveMs, uncertaintyMs: Number((hostReceived-sent)-(hostSent-received))/1e6 };
}

export function frameLatencyBound(frame, bound, maxDriftPPM) {
  if (!Number.isFinite(maxDriftPPM) || maxDriftPPM < 0 || maxDriftPPM > 1000) throw new Error("clock_drift_bound_missing");
  const elapsed = frame.browserObservedMs-bound.observedAtMs;
  if (!Number.isFinite(elapsed) || elapsed < 0 || elapsed > PERFORMANCE_REQUIREMENTS.maxClockAgeMs) throw new Error("stale_clock_bound");
  const drift = BigInt(Math.ceil(elapsed * maxDriftPPM)); // ms * ppm == ns
  const observed = browserNS(frame.browserObservedMs), painted = ns(frame.fixtureDrawHostNs);
  const lower = observed+BigInt(bound.lowerOffsetNs)-drift-painted;
  const upper = observed+BigInt(bound.upperOffsetNs)+drift-painted;
  if (upper < 0n) throw new Error("frame_from_future_clock");
  return { method: "fixture-draw-to-browser-observed-upper-bound", lowerMs: Number(lower)/1e6, upperMs: Number(upper)/1e6, clockUncertaintyMs: Number(BigInt(bound.upperOffsetNs)-BigInt(bound.lowerOffsetNs)+2n*drift)/1e6, physicalScanoutVerified: false };
}

export function percentile(values, quantile) {
  if (!values.length || values.some((value) => !Number.isFinite(value)) || quantile <= 0 || quantile > 1) throw new Error("missing_metric_samples");
  const sorted = [...values].sort((a,b)=>a-b); return sorted[Math.ceil(sorted.length*quantile)-1];
}

export function memoryTrend(samples, warmupMs = 300_000) {
  if(!Array.isArray(samples))throw new Error("memory_samples_unavailable");
  const usable=samples.filter((s)=>s.elapsedMs>=warmupMs);
  if(usable.length<60||usable.some((s,i)=>!Number.isFinite(s.elapsedMs)||!Number.isFinite(s.rssBytes)||s.rssBytes<0||(i&&(s.elapsedMs<=usable[i-1].elapsedMs||s.elapsedMs-usable[i-1].elapsedMs>20000))))throw new Error("memory_coverage_incomplete");
  const buckets=new Map();for(const sample of usable){const minute=Math.floor((sample.elapsedMs-warmupMs)/60000);if(!buckets.has(minute))buckets.set(minute,[]);buckets.get(minute).push(sample.rssBytes);}
  const blocks=[...buckets].filter(([,values])=>values.length>=3).map(([minute,values])=>({x:minute,y:percentile(values,0.5)}));
  if(blocks.length<20)throw new Error("memory_blocks_incomplete");
  const regress=(points)=>{const mx=points.reduce((s,p)=>s+p.x,0)/points.length,my=points.reduce((s,p)=>s+p.y,0)/points.length,xx=points.reduce((s,p)=>s+(p.x-mx)**2,0);if(!xx)throw new Error("invalid_resource_timeline");const slope=points.reduce((s,p)=>s+(p.x-mx)*(p.y-my),0)/xx,residuals=points.map((p)=>p.y-my-slope*(p.x-mx));const uncertainty=2.2*Math.sqrt(residuals.reduce((s,r)=>s+r*r,0)/(points.length-2)/xx);return {slope,lower:slope-uncertainty,upper:slope+uncertainty,residuals};};
  const full=regress(blocks),tail=regress(blocks.slice(-10));
  const noise=3*1.4826*percentile(full.residuals.map(Math.abs),0.5);
  const endChange=percentile(blocks.slice(-5).map((p)=>p.y),0.5)-percentile(blocks.slice(0,5).map((p)=>p.y),0.5);
  const span=blocks.at(-1).x-blocks[0].x;
  const sustained=full.lower>0&&tail.lower>0;
  const stable=full.upper<=0||(full.lower<=0&&Math.abs(endChange)<=noise&&Math.max(0,full.upper)*span<=2*noise);
  return {status:sustained?"sustained-growth":stable?"no-detectable-sustained-growth":"inconclusive",bytesPerMinute:full.slope,lower95BytesPerMinute:full.lower,upper95BytesPerMinute:full.upper,tailLower95BytesPerMinute:tail.lower,noiseEnvelopeBytes:noise,endMedianGrowthBytes:endChange,warmupExcludedMs:warmupMs,minuteBlocks:blocks.length,method:"one-minute medians, OLS 95% interval and MAD noise envelope; not proof of zero allocation"};
}

// Marker layout: 16-bit magic, 32-bit source tag, 32-bit frame ID, 64-bit
// host-monotonic draw time, CRC16. Producer geometry is explicit, never guessed.
export function decodeFrameMarker(rgba, imageWidth, imageHeight, layout) {
  const { x,y,cellSize,columns }=layout;
  if (![x,y,cellSize,columns,imageWidth,imageHeight].every(Number.isInteger) || x<0 || y<0 || cellSize<3 || columns<1 || rgba.length!==imageWidth*imageHeight*4) throw new Error("invalid_marker_geometry");
  const bitCount=160, rows=Math.ceil(bitCount/columns);
  if (x+columns*cellSize>imageWidth || y+rows*cellSize>imageHeight) throw new Error("marker_outside_frame");
  const bytes=new Uint8Array(bitCount/8);
  for(let bit=0;bit<bitCount;bit++){
    const cx=x+(bit%columns)*cellSize+Math.floor(cellSize/2), cy=y+Math.floor(bit/columns)*cellSize+Math.floor(cellSize/2);
    let luminance=0;for(let dy=-1;dy<=1;dy++)for(let dx=-1;dx<=1;dx++){const i=((cy+dy)*imageWidth+cx+dx)*4;luminance+=rgba[i]+rgba[i+1]+rgba[i+2];}
    if(luminance/(9*3)>127)bytes[Math.floor(bit/8)]|=1<<(7-bit%8);
  }
  if(bytes[0]!==0x56 || bytes[1]!==0x53)throw new Error("marker_not_observed");
  let crc=0xffff;for(const value of bytes.subarray(0,18)){crc^=value<<8;for(let i=0;i<8;i++)crc=(crc&0x8000)?((crc<<1)^0x1021)&0xffff:(crc<<1)&0xffff;}
  if(crc!==((bytes[18]<<8)|bytes[19]))throw new Error("marker_crc_failed");
  const view=new DataView(bytes.buffer);let stamp=0n;for(const value of bytes.subarray(10,18))stamp=(stamp<<8n)|BigInt(value);
  return { sourceTag:view.getUint32(2), frameID:view.getUint32(6), fixtureDrawHostNs:stamp.toString() };
}

export function evaluatePerformance(evidence) {
  const failed=[];const require=(condition,reason)=>{if(!condition)failed.push(reason);};const p=PERFORMANCE_REQUIREMENTS;
  require(evidence.schemaVersion===1,"metric_schema_unavailable");
  require(!evidence.failure,evidence.failure??"execution_failure");
  require(evidence.mode==="acceptance","debug_run_not_acceptance");
  require(evidence.provenance?.native==="selected-bundle" && evidence.provenance?.renderer==="chromium-rvfc-canvas" && evidence.provenance?.synthetic===false,"real_capture_render_provenance_missing");
  const remoteNetwork=evidence.networkScope==="remote-lan-candidate"?evaluateRemoteNetwork(evidence.networkEvidence,evidence.viewers??[],evidence.durationMs):null;
  require(remoteNetwork?.verified===true || evidence.networkScope==="loopback" && evidence.networkEvidence?.kind==="same-host-private-session","network_provenance_unavailable");
  require(evidence.durationMs>=p.durationMs && evidence.workload==="dynamic-owned-fixture","duration_or_dynamic_workload_incomplete");
  require(evidence.requested?.width===p.width && evidence.requested?.height===p.height && evidence.requested?.fps===p.fps,"requested_quality_mismatch");
  const viewers=evidence.viewers ?? [];require(viewers.length===p.viewers,"two_viewers_required");
  require(new Set(viewers.map((v)=>v.viewerID)).size===p.viewers && new Set(viewers.map((v)=>v.sourceID)).size===1,"same_source_independent_viewers_required");
  const viewerMetrics=[];
  for(const viewer of viewers){
    require(viewer.negotiated?.width===p.width && viewer.negotiated?.height===p.height && viewer.negotiated?.fps===p.fps,"negotiated_quality_below_target");
    require(viewer.observedDurationMs>=p.durationMs && viewer.renderedFrames>=p.minRenderedFps*viewer.observedDurationMs/1000 && viewer.uniqueDynamicFrames>=p.minRenderedFps*viewer.observedDurationMs/1000 && viewer.decodedFrames>=p.minRenderedFps*viewer.observedDurationMs/1000,"decoded_rendered_fps_below_target");
    require(viewer.staleSourceFrames===0 && viewer.invalidMarkers===0,"frame_correlation_failed");
    try{
      require(viewer.latencyUpperBoundsMs?.length>=p.minRenderedFps*p.durationMs/1000,"latency_sample_coverage_incomplete");
      const p95=percentile(viewer.latencyUpperBoundsMs,0.95);require(viewer.latencyMethod==="fixture-draw-to-browser-observed-upper-bound" && Number.isFinite(viewer.maxClockUncertaintyMs),"clock_or_latency_method_unavailable");require(p95<=p.latencyP95Ms,"latency_upper_bound_above_target");
      const switchP95=percentile(viewer.switchFirstDecodedMs,0.95);require(viewer.switchFirstDecodedMs.length>=30 && switchP95<=p.switchP95Ms,"first_decoded_switch_above_target");
      viewerMetrics.push({viewerID:viewer.viewerID,renderedFps:viewer.renderedFrames/(viewer.observedDurationMs/1000),uniqueDynamicFps:viewer.uniqueDynamicFrames/(viewer.observedDurationMs/1000),decodedFps:viewer.decodedFrames/(viewer.observedDurationMs/1000),latencyUpperP95Ms:p95,switchFirstDecodedP95Ms:switchP95});
    }catch{failed.push("latency_or_switch_samples_unavailable");}
  }
  require(evidence.sharedSource?.captureSessions===1 && evidence.sharedSource?.encoderSessions===1 && evidence.sharedSource?.independentlyObserved===true,"shared_capture_encoder_unverified");
  require(evidence.concurrentProbe?.runtimeIDs?.length===2 && new Set(evidence.concurrentProbe.runtimeIDs).size===2 && evidence.concurrentProbe?.sourceIDs?.length===2 && new Set(evidence.concurrentProbe.sourceIDs).size===2 && evidence.concurrentProbe?.rendered===true,"two_runtime_source_probe_incomplete");
  const resources=evidence.resources ?? {};const trends={};
  for(const process of ["native","go"]){
    const resource=resources[process];
    for(const key of ["rss","cpu","fd","callbacks","encoder","bandwidth"]) require(resource?.availability?.[key]?.available===true,`${process}_${key}_unavailable`);
    require(resource?.samples?.every((s,index,all)=>Number.isFinite(s.rssBytes)&&s.rssBytes>=0&&Number.isInteger(s.fdCount)&&s.fdCount>=0&&Number.isInteger(s.activeCallbacks)&&s.activeCallbacks>=0&&Number.isInteger(s.activeEncoders)&&s.activeEncoders>=0&&Number.isFinite(s.bytesSentTotal)&&s.bytesSentTotal>=0&&/^\d+$/.test(s.cpuTimeNs??"")&&(!index||(BigInt(s.cpuTimeNs)>=BigInt(all[index-1].cpuTimeNs)&&s.bytesSentTotal>=all[index-1].bytesSentTotal))),`${process}_resource_values_unavailable`);
    require(resource?.samples?.length>=180 && resource.samples.at(-1)?.elapsedMs>=p.durationMs,`${process}_resource_coverage_incomplete`);
    try{const trend=memoryTrend(resource.samples);trends[process]=trend;require(trend.status==="no-detectable-sustained-growth",`${process}_${trend.status}`);}catch{failed.push(`${process}_memory_trend_unavailable`);}
  }
  const gpu=evidence.systemGPU;
  const validGPU=(samples)=>Array.isArray(samples)&&samples.every((s)=>Number.isFinite(s.elapsedMs)&&[s.deviceUtilizationPercent,s.rendererUtilizationPercent,s.tilerUtilizationPercent].every((n)=>Number.isFinite(n)&&n>=0&&n<=100)&&Number.isFinite(s.inUseSystemMemoryBytes)&&s.inUseSystemMemoryBytes>=0);
  require(gpu?.scope==="system"&&gpu?.attribution==="not_benchmark_specific"&&gpu?.availability?.available===true&&typeof gpu.method==="string"&&gpu.method.length>0&&gpu.baseline?.length>=3&&validGPU(gpu.baseline)&&gpu.samples?.length>=180&&validGPU(gpu.samples)&&gpu.samples.at(-1).elapsedMs>=p.durationMs,"system_gpu_observation_unavailable");
  const cycles=evidence.cycles ?? [];require(cycles.length>=p.cycles,"thirty_lifecycle_cycles_required");
  require(cycles.length>0 && cycles.every((c)=>c.captureOpened===true&&c.encodedFrameReceived===true&&c.disposed===true&&c.fixtureExited===true&&c.managedDisplaysAfter===0&&c.activeCallbacksAfter===0&&c.activeEncodersAfter===0&&c.fdDelta<=0&&c.measurementsAvailable===true),"cleanup_residue_or_metrics_unavailable");
  require(evidence.cleanupConfirmed===true,"performance_cleanup_unconfirmed");
  return {status:remoteNetwork?.verified===true && failed.length===0?"passed":"blocked",remoteNetwork,localMeasurement:{status:failed.length?"blocked":"passed",failed:[...new Set(failed)]},scope:"capture-to-browser-observed-upper-bound; physical scanout not measured",networkScope:evidence.networkScope ?? "unavailable",gpuScope:"system-not-attributable-to-benchmark",planCoverage:{lanAcceptance:remoteNetwork?.verified===true?"verified":"unverified",physicalScanout:"unverified",remainingUnit:remoteNetwork?.verified===true?null:"authorized second-machine execution with verified LAN candidate pair and machine identity"},requirements:p,failed:[...new Set([...failed,...(remoteNetwork?.verified===true?[]:["lan_acceptance_unverified"]),...(remoteNetwork?.reasons??[])])],viewers:viewerMetrics,memoryTrends:trends};
}
