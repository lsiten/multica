import { createRequire } from "node:module";
import { clockBounds, decodeFrameMarker, frameLatencyBound } from "./vscreen-performance-metrics.mjs";

const requireRootTool = createRequire(new URL("../../../package.json", import.meta.url));
export function loadPerformanceChromium() { return requireRootTool("@playwright/test").chromium; }

// Requires a caller-owned Playwright page. This module never launches or attaches
// a native capture process and has no ambient browser/account lookup.
export async function startPerformanceViewer(page, config) {
  const endpoint = new URL(config.baseURL);
  if (endpoint.protocol !== "http:" || endpoint.hostname !== "127.0.0.1" || !config.nonce || !config.viewerID) throw new Error("private_performance_session_required");
  // A private same-origin document avoids Origin:null CORS/preflight requests.
  // Only this exact navigation is fulfilled; API requests reach the authenticated producer.
  const viewerURL = new URL("/viewer", endpoint).href;
  const shell = (route) => route.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><html><head><meta charset=\"utf-8\"></head><body></body></html>" });
  await page.route(viewerURL, shell, { times: 1 });
  try {
    await page.goto(viewerURL, { waitUntil: "domcontentloaded" });
    await page.evaluate(browserPerformanceProbe, { ...config, decodeSource:decodeFrameMarker.toString() });
  } finally {
    await page.unroute(viewerURL, shell);
  }
  return {
    sample:()=>page.evaluate(()=>window.__vscreenPerformance.sample()),
    switchSource:(source,phase)=>page.evaluate(({source,phase})=>window.__vscreenPerformance.switchSource(source,phase),{source,phase}),
    close:()=>page.evaluate(()=>window.__vscreenPerformance.close()),
  };
}

// All measurements below are taken from actual decoded video frames. DataChannel
// metadata is not frame-synchronous and is intentionally never used for latency.
async function browserPerformanceProbe(config) {
  const decode = (0,eval)(`(${config.decodeSource})`);
  const video=document.createElement("video");video.autoplay=true;video.muted=true;video.playsInline=true;video.style.width="800px";document.body.append(video);
  const canvas=document.createElement("canvas"), drawing=canvas.getContext("2d",{willReadFrequently:true});
  if(!video.requestVideoFrameCallback || !drawing || !window.RTCPeerConnection)throw new Error("actual_video_frame_api_unavailable");
  let pc=null, source=config.source, negotiated=null, marker=null, sourceTag=null, frameCallback=null, phase="steady", stopped=false;
  let firstAt=null, latestAt=null, openedAt=performance.now(), rendered=0, invalid=0, stale=0, unique=0, lastFrameID=null, decoded=0, bytes=0, cursor=0;
  let frames=[], clockSamples=[], pendingClock=false, failure=null, switches=[], switchPending=false;
  const request=async(path,body)=>{
    const controller=new AbortController(), timer=setTimeout(()=>controller.abort(),10000);
    try{const response=await fetch(config.baseURL+path,{method:body===undefined?"GET":"POST",headers:{Authorization:`Bearer ${config.nonce}`,...(body===undefined?{}:{"Content-Type":"application/json"})},body:body===undefined?undefined:JSON.stringify(body),signal:controller.signal});if(!response.ok)throw new Error(`performance_endpoint_${response.status}`);return await response.json();}finally{clearTimeout(timer);}
  };
  const synchronize=async()=>{if(pendingClock||stopped)return;pendingClock=true;try{const sent=performance.now(),host=await request("/clock");clockSamples.push({browserSendMs:sent,browserReceiveMs:performance.now(),hostReceiveNs:host.host_receive_ns,hostSendNs:host.host_send_ns,clockEpoch:host.clock_epoch});if(clockSamples.length>256)clockSamples.shift();}catch(error){failure=error.message;}finally{pendingClock=false;}};
  const onFrame=()=>{
    if(stopped)return;
    try{
      if(marker && video.videoWidth && video.videoHeight){
        if(video.videoWidth!==negotiated.width || video.videoHeight!==negotiated.height)throw new Error("decoded_dimensions_mismatch");
        const width=marker.columns*marker.cell_size,height=Math.ceil(160/marker.columns)*marker.cell_size;
        canvas.width=width;canvas.height=height;
        drawing.drawImage(video,marker.x,marker.y,width,height,0,0,width,height);
        const decodedMarker=decode(drawing.getImageData(0,0,width,height).data,width,height,{x:0,y:0,cellSize:marker.cell_size,columns:marker.columns});
        const observed=performance.now();
        if(decodedMarker.sourceTag!==sourceTag){stale++;}else{
          if(firstAt===null)firstAt=observed;latestAt=observed;rendered++;
          if(lastFrameID!==decodedMarker.frameID){unique++;lastFrameID=decodedMarker.frameID;}
          if(switchPending){switches.push(observed-openedAt);switchPending=false;}
          if(frames.length>=60000)throw new Error("render_sample_capacity_exceeded");
          frames.push({phase,sourceID:source.source_id,sourceTag,frameID:decodedMarker.frameID,fixtureDrawHostNs:decodedMarker.fixtureDrawHostNs,browserObservedMs:observed});
        }
      }
    }catch(error){if(firstAt!==null)invalid++;if(["render_sample_capacity_exceeded","decoded_dimensions_mismatch"].includes(error.message))failure=error.message;}
    frameCallback=video.requestVideoFrameCallback(onFrame);
  };
  const closePeer=async()=>{if(pc){pc.close();pc=null;await request("/viewer/close",{viewer_id:config.viewerID});}video.srcObject=null;};
  const connect=async(next, nextPhase)=>{
    marker=null;sourceTag=null;await closePeer();source=next;phase=nextPhase;lastFrameID=null;openedAt=performance.now();switchPending=nextPhase==="switch";
    pc=new RTCPeerConnection();pc.createDataChannel("mirror-control",{ordered:true});
    const transceiver=pc.addTransceiver("video",{direction:"recvonly"});
    const codecs=RTCRtpReceiver.getCapabilities("video")?.codecs.filter((c)=>c.mimeType.toLowerCase()==="video/h264");
    if(!codecs?.length)throw new Error("chromium_h264_decode_unavailable");transceiver.setCodecPreferences(codecs);
    pc.ontrack=(event)=>{video.srcObject=new MediaStream([event.track]);video.play().catch((error)=>{failure=error.message;});};
    await pc.setLocalDescription(await pc.createOffer());
    await new Promise((resolve,reject)=>{if(pc.iceGatheringState==="complete"){resolve();return;}const timeout=setTimeout(()=>reject(new Error("browser_ice_timeout")),5000);pc.addEventListener("icegatheringstatechange",()=>{if(pc.iceGatheringState==="complete"){clearTimeout(timeout);resolve();}});});
    const answer=await request("/offer",{viewer_id:config.viewerID,source_id:source.source_id,offer:pc.localDescription.toJSON()});
    negotiated=answer.negotiated;marker=answer.marker;sourceTag=answer.source_tag;
    if(!marker||!Number.isInteger(sourceTag))throw new Error("frame_marker_contract_missing");
    await pc.setRemoteDescription(answer.answer);
  };
  await synchronize();await connect(source,"steady");frameCallback=video.requestVideoFrameCallback(onFrame);
  const clockTimer=setInterval(synchronize,10000);
  const renewTimer=setInterval(()=>request("/renew",{viewer_id:config.viewerID}).catch((error)=>{failure=error.message;}),10000);
  window.__vscreenPerformance={
    async sample(){if(pc){const stats=await pc.getStats();for(const stat of stats.values())if(stat.type==="inbound-rtp"&&stat.kind==="video"){decoded=stat.framesDecoded??null;bytes=stat.bytesReceived??null;}}
      const batch=frames.slice(cursor);cursor=frames.length;
      return {viewerID:config.viewerID,sourceID:source.source_id,phase,negotiated,frames:batch,clockSamples:[...clockSamples],observedDurationMs:firstAt===null?0:latestAt-firstAt,renderedFrames:rendered,uniqueDynamicFrames:unique,decodedFrames:decoded,bytesReceived:bytes,invalidMarkers:invalid,staleSourceFrames:stale,switchFirstDecodedMs:[...switches],failure};},
    switchSource:connect,
    async close(){stopped=true;clearInterval(clockTimer);clearInterval(renewTimer);if(frameCallback!==null)video.cancelVideoFrameCallback(frameCallback);try{await closePeer();}finally{video.remove();}}
  };
}

export function correlateRenderedBatch(batch, maxDriftPPM = 100) {
  if(batch.failure)throw new Error(batch.failure);
  const epochs=new Set(batch.clockSamples.map((sample)=>sample.clockEpoch));if(epochs.size!==1 || epochs.has(undefined))throw new Error("clock_epoch_changed_or_missing");
  const bounds=batch.clockSamples.map(clockBounds);
  for(let i=1;i<bounds.length;i++){const delta=bounds[i].observedAtMs-bounds[i-1].observedAtMs;if(delta<0)throw new Error("clock_sample_order_invalid");const drift=BigInt(Math.ceil(delta*maxDriftPPM));if(BigInt(bounds[i].lowerOffsetNs)>BigInt(bounds[i-1].upperOffsetNs)+drift||BigInt(bounds[i].upperOffsetNs)<BigInt(bounds[i-1].lowerOffsetNs)-drift)throw new Error("clock_offset_discontinuity");}
  return batch.frames.map((frame)=>{
    const bound=[...bounds].reverse().find((sample)=>sample.observedAtMs<=frame.browserObservedMs);
    if(!bound)throw new Error("frame_without_clock_bound");
    return {...frame,...frameLatencyBound(frame,bound,maxDriftPPM)};
  });
}
