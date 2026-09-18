import { isIP } from "node:net";
const fingerprint = (s) => typeof s === "string" ? s.replaceAll(":", "").toLowerCase() : "";
const hash = (s) => /^[a-f0-9]{64}$/.test(s ?? "");
const direct = (c) => c?.candidate_type === "host" && c.protocol === "udp" && isIP(c.address) > 0 && !["::1", "0.0.0.0", "::"].includes(c.address) && !c.address.startsWith("127.") && Number.isInteger(c.port) && c.port > 0 && c.port < 65536;
const equal = (a, b) => a?.address === b?.address && a?.port === b?.port && a?.protocol === b?.protocol;
function routeMatches(route, local, remote) {
  return local && remote && route?.available === true && route.kind === "lan" && typeof route.method === "string" && route.method.length > 0 && typeof route.interface_name === "string" && !/^(lo|utun|tun|tap|wg|tailscale|docker|veth|br|bridge)/.test(route.interface_name) && route.local_address === local?.address && route.destination === remote?.address && route.interface_addresses?.includes(local.address);
}
function connectionReasons(native, browser, viewer) {
  if(!native || !browser)return ["lan_viewer_missing"];
  const reasons=[],require=(ok,reason)=>{if(!ok)reasons.push(reason);},np=native.selected_pair,bp=browser.selected_pair;
  require(native.viewer_id===viewer.viewerID && browser.viewer_id===viewer.viewerID && typeof np?.pair_id==="string" && np.pair_id.length>0 && typeof bp?.id==="string" && bp.id.length>0,"lan_connection_identity_missing");
      require(native.available === true && native.source_id === viewer.sourceID && browser.source_id === viewer.sourceID && typeof native.grant_id === "string" && native.grant_id.length > 0, "lan_scope_mismatch");
      require([np, bp].every((pair) => pair?.state === "succeeded" && pair.nominated === true && direct(pair.local) && direct(pair.remote) && Number.isFinite(pair.bytes_sent) && pair.bytes_sent > 0 && Number.isFinite(pair.bytes_received) && pair.bytes_received > 0) && equal(np?.local, bp?.remote) && equal(np?.remote, bp?.local), "lan_selected_pair_mismatch");
      require(/^\d{1,20}$/.test(native.observed_host_ns ?? "") && Number.isFinite(browser.observed_browser_ms) && browser.observed_browser_ms >= 0, "lan_observation_clock_missing");
      const n = native.dtls, d = browser.dtls;
      require(n?.available === true && n.algorithm === "sha-256" && d?.state === "connected" && d.local?.algorithm === "sha-256" && d.remote?.algorithm === "sha-256" && hash(fingerprint(n.local_fingerprint)) && hash(fingerprint(n.remote_fingerprint)) && fingerprint(n.local_fingerprint) === fingerprint(d.remote.fingerprint) && fingerprint(n.remote_fingerprint) === fingerprint(d.local.fingerprint), "lan_dtls_identity_mismatch");
      require(routeMatches(native.route, np?.local, np?.remote) && routeMatches(browser.route, bp?.local, bp?.remote), "lan_physical_route_unverified");
  return reasons;
}
export function evaluateRemoteNetwork(evidence, viewers, durationMs) {
  const reasons = [], require = (ok, reason) => { if (!ok) reasons.push(reason); };
  require(evidence?.kind === "controlled-ssh-worker" && hash(evidence.run_id) && hash(evidence.worker_hash) && evidence.worker_hash === evidence.local_worker_hash && /^SHA256:[A-Za-z0-9+/]{43}$/.test(evidence.ssh_host_key ?? "") && evidence.fresh_contexts === 2 && typeof evidence.browser_version === "string", "remote_provenance_incomplete");
  const host = evidence?.host_identity, remote = evidence?.remote_identity;
  require([host, remote].every((i) => i && i.challenge === evidence.run_id && hash(i.machine_id_hash) && ["darwin", "linux"].includes(i.os) && i.method === (i.os === "darwin" ? "IOPlatformUUID" : "etc-machine-id") && i.assurance === "trusted-os-report-not-hardware-attestation") && host?.machine_id_hash !== remote?.machine_id_hash, "independent_machine_identity_unverified");
  require(Array.isArray(viewers) && viewers.length === 2 && new Set(viewers.map((v)=>v.viewerID)).size === 2, "lan_two_viewers_required");
  viewers = Array.isArray(viewers) ? viewers : [];
  const groups = Array.isArray(evidence?.observations) ? evidence.observations : [], previous = new Map(), identities = new Map(); let last = -1, first = null, epoch = null;
  require(groups.length >= 180 && groups.length <= 4000, "lan_sample_coverage_incomplete");
  for (const group of groups) {
    if(!group || typeof group!=="object"){reasons.push("lan_observation_invalid");continue;}
    require(Number.isFinite(group.elapsed_ms) && group.elapsed_ms > last && (last < 0 || group.elapsed_ms - last <= 15000), "lan_observation_gap");
    first ??= group.elapsed_ms; last = group.elapsed_ms;
    const producer = group.producer; epoch ??= producer?.run_id;
    require(producer?.schema_version === 1 && typeof epoch === "string" && epoch.length > 0 && producer.run_id === epoch && group.clock_epoch === epoch, "lan_producer_epoch_mismatch");
    for (const viewer of viewers) {
      const p = (Array.isArray(producer?.viewers)?producer.viewers:[]).filter((v) => v?.viewer_id === viewer.viewerID), b = (Array.isArray(group.browsers)?group.browsers:[]).filter((v) => v?.viewer_id === viewer.viewerID);
      if (p?.length !== 1 || b?.length !== 1) { reasons.push("lan_viewer_missing"); continue; }
      const native = p[0], browser = b[0], np = native.selected_pair, bp = browser.selected_pair;
      reasons.push(...connectionReasons(native,browser,viewer));
      const key = `${viewer.viewerID}/${native.grant_id}/${np?.pair_id}/${bp?.id}`, before = previous.get(key);
      require(!identities.has(viewer.viewerID) || identities.get(viewer.viewerID)===key,"lan_peer_changed_during_steady_phase");identities.set(viewer.viewerID,key);
      if (before) require(np && bp && /^\d{1,20}$/.test(native.observed_host_ns ?? "") && BigInt(native.observed_host_ns) > before.host && browser.observed_browser_ms > before.browser && np.bytes_sent >= before.ns && np.bytes_received >= before.nr && bp.bytes_received >= before.br && bp.bytes_sent >= before.bs && (np.bytes_sent>before.ns && bp.bytes_received>before.br || group.elapsed_ms-before.progress<=15000), "lan_counters_not_progressing");
      previous.set(key, { progress: !before || np?.bytes_sent>before.ns && bp?.bytes_received>before.br ? group.elapsed_ms : before.progress, host: /^\d{1,20}$/.test(native.observed_host_ns ?? "") ? BigInt(native.observed_host_ns) : 0n, browser: browser.observed_browser_ms, ns: np?.bytes_sent, nr: np?.bytes_received, br: bp?.bytes_received, bs: bp?.bytes_sent });
    }
  }
  require(last - first >= durationMs - 5000 && first <= 15000, "lan_duration_unverified");
  const switches=Array.isArray(evidence?.switch_observations)?evidence.switch_observations:[],grants=new Map(),counts=new Map();
  const sources=Array.isArray(evidence?.sources)?evidence.sources:[];
  require(sources.length===2 && new Set(sources.map((source)=>source?.source_id)).size===2,"lan_sources_missing");
  for(const viewer of viewers){const lastViewers=groups.at(-1)?.producer?.viewers;const native=Array.isArray(lastViewers)?lastViewers.find((v)=>v?.viewer_id===viewer.viewerID):undefined;grants.set(viewer.viewerID,native?.grant_id);}
  for(const item of switches){
    const source=sources.find((s)=>s?.source_id===item?.source_id),viewer=viewers.find((v)=>v.viewerID===item?.viewer_id);
    if(!source||!viewer){reasons.push("lan_switch_scope_mismatch");continue;}
    require(item.clock_epoch===epoch && item.producer_epoch===epoch && item.source_tag===source.source_tag && item.frame_source_tag===source.source_tag,"lan_switch_frame_binding_mismatch");
    reasons.push(...connectionReasons(item.native,item.browser,{viewerID:viewer.viewerID,sourceID:source.source_id}));
    require(item.native?.grant_id!==grants.get(viewer.viewerID),"lan_switch_old_grant");grants.set(viewer.viewerID,item.native?.grant_id);counts.set(viewer.viewerID,(counts.get(viewer.viewerID)??0)+1);
  }
  require(switches.length===60 && viewers.every((v)=>counts.get(v.viewerID)===30),"lan_switch_network_coverage_incomplete");
  require(evidence?.cleanup_confirmed === true, "remote_cleanup_unconfirmed");
  return { verified: reasons.length === 0, reasons: [...new Set(reasons)], assurance: "pinned-ssh-and-trusted-os-worker; no hardware attestation" };
}
