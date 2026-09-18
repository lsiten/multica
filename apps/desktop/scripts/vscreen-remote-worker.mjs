import { remoteRoute } from "./vscreen-remote-route.mjs";
import { pathToFileURL } from "node:url";
import { remoteChannel } from "./vscreen-remote-protocol.mjs";
import { machineIdentity, remoteWorkerHash } from "./vscreen-remote-identity.mjs";
import { loadPerformanceChromium, startPerformanceViewer } from "./vscreen-performance-browser.mjs";

export function runRemoteViewerWorker(input, output, dependencies = {}) {
  let runID = null, browser = null, starting = false, stopped = false, sources = [];
  const peers = new Map(); let channel;
  const stop = async () => { stopped = true; let clean = true; for (const { peer, context } of peers.values()) { try { await peer.close(); } catch { clean = false; } try { await context.close(); } catch { clean = false; } } peers.clear(); if (browser) { try { await browser.close(); } catch { clean = false; } browser = null; } return { cleanup_confirmed: clean && !starting }; };
  const scope = (body) => { if (!runID || stopped || body?.run_id !== runID) throw new Error("remote_scope_invalid"); };
  const handlers = {
    async hello(body) { if (runID || !/^[a-f0-9]{64}$/.test(body?.run_id) || body.worker_hash !== await remoteWorkerHash()) throw new Error("remote_identity_invalid"); runID = body.run_id; return { run_id: runID, worker_hash: await remoteWorkerHash(), identity: await (dependencies.machineIdentity ?? machineIdentity)(runID), pid: process.pid }; },
    async start(body) {
      scope(body); if (browser || starting || !Array.isArray(body.sources) || body.sources.length !== 2 || body.sources.some((s) => typeof s.source_id !== "string" || s.source_id.length > 256 || !Number.isInteger(s.source_tag))) throw new Error("remote_start_invalid");
      starting = true; sources = body.sources;
      try {
        browser = await (dependencies.chromium ?? loadPerformanceChromium()).launch({ headless: true });
        if (stopped) throw new Error("remote_stopped");
        for (let index = 0; index < 2; index++) {
          const viewerID = `performance-${index}`, context = await browser.newContext({ viewport: { width: 1700, height: 1000 } });
          try {
            const peer = await startPerformanceViewer(await context.newPage(), { baseURL: "http://127.0.0.1:1", viewerID, source: sources[0] }, (path, value) => channel.request("signal", { run_id: runID, viewer_id: viewerID, path, body: value }));
            if (stopped) { await peer.close(); throw new Error("remote_stopped"); }
            peers.set(viewerID, { context, peer });
          } catch (error) { await context.close(); throw error; }
        }
        return { viewer_ids: [...peers.keys()], browser_version: browser.version(), fresh_contexts: 2 };
      } catch (error) { await stop(); throw error; } finally { starting = false; }
    },
    async sample(body) { scope(body); const item = peers.get(body.viewer_id); if (!item) throw new Error("remote_viewer_invalid"); const sample = await item.peer.sample(); if(sample.network) sample.network.route = await (dependencies.remoteRoute ?? remoteRoute)(sample.network.selected_pair); return sample; },
    async switch(body) { scope(body); const item = peers.get(body.viewer_id), source = sources.find((s) => s.source_id === body.source_id); if (!item || !source || !["switch", "concurrent"].includes(body.phase)) throw new Error("remote_switch_invalid"); await item.peer.switchSource(source, body.phase); return { switched: true }; },
    async close(body) { scope(body); return stop(); },
  };
  channel = remoteChannel(input, output, handlers, { onClose: () => { void stop(); } });
  return { channel, stop };
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  if (process.argv.length === 3 && process.argv[2] === "--hash") console.log(await remoteWorkerHash());
  else if (process.argv.length !== 3 || process.argv[2] !== "--stdio") process.exitCode = 1;
  else { const worker = runRemoteViewerWorker(process.stdin, process.stdout); const deadline = setTimeout(() => { void worker.stop().finally(() => process.exit(1)); }, 2700000); deadline.unref(); process.once("SIGTERM", () => { void worker.stop().finally(() => process.exit(1)); }); process.once("SIGINT", () => { void worker.stop().finally(() => process.exit(1)); }); }
}
