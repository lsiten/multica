import { installSyntheticSender } from "./vscreen-performance-headless.fixture.mjs";
// @vitest-environment node
import { writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { performance as nodePerformance } from "node:perf_hooks";
import { expect, it } from "vitest";
import { loadPerformanceChromium, startPerformanceViewer } from "./vscreen-performance-browser.mjs";

// Exercise the same root-declared Playwright loader as the default runner. No
// browser download, user profile, external codec or native capture is used.
it.skipIf(process.env.VSCREEN_RUN_HEADLESS_SMOKE !== "1")("real Chromium uses producer-compatible origin/auth and observes synthetic H264 marker frames across switch", async () => {
  const chromium = loadPerformanceChromium();
  const browser = await chromium.launch({ headless: true });
  const producerContext = await browser.newContext();
  const sender = await producerContext.newPage();
  const viewerContext = await browser.newContext({ viewport: { width: 900, height: 700 } });
  const viewerPage = await viewerContext.newPage();
  const nonce = "owned-headless-only-" + "a".repeat(48);
  const requests = [];
  let baseURL;
  let producer;
  let declaredWidth = 640;
  let observedEvidence;
  const server = createServer(async (req, res) => {
    const origin = req.headers.origin ?? "";
    requests.push({ path: req.url, method: req.method, origin, authenticated: req.headers.authorization === `Bearer ${nonce}` });
    if (origin !== "" && origin !== baseURL) { res.writeHead(403); res.end("origin refused"); return; }
    if (req.method === "OPTIONS") { res.writeHead(204, { "Access-Control-Allow-Origin": origin, "Access-Control-Allow-Methods": "GET, POST", "Access-Control-Allow-Headers": "Authorization, Content-Type" }); res.end(); return; }
    if (req.headers.authorization !== `Bearer ${nonce}`) { res.writeHead(401); res.end(); return; }
    res.setHeader("Content-Type", "application/json");
    try {
      if (req.url === "/clock") {
        const received = BigInt(Math.round((nodePerformance.timeOrigin + nodePerformance.now()) * 1e6)).toString();
        res.end(JSON.stringify({ host_receive_ns: received, host_send_ns: BigInt(Math.round((nodePerformance.timeOrigin + nodePerformance.now()) * 1e6)).toString(), clock_epoch: "synthetic-node-clock-not-native" })); return;
      }
      const chunks = []; for await (const chunk of req) chunks.push(chunk); const body = JSON.parse(Buffer.concat(chunks).toString() || "{}");
      if (req.url === "/offer") {
        const answer = await sender.evaluate(async ({ offer, source }) => window.ownedSyntheticSender.offer(offer, source), { offer: body.offer, source: body.source_id });
        res.end(JSON.stringify({ answer, source_id: body.source_id, source_tag: body.source_id === "source-a" ? 1 : 2, marker: { x: 8, y: 8, cell_size: 8, columns: 20 }, negotiated: { width: declaredWidth, height: 360, fps: 30 } })); return;
      }
      if (req.url === "/viewer/close") { await sender.evaluate(() => window.ownedSyntheticSender.close()); res.end('{"ok":true}'); return; }
      if (req.url === "/renew") { res.end('{"ok":true}'); return; }
      res.writeHead(404); res.end("{}");
    } catch (error) { console.log("Owned synthetic sender error:", error.message); if (!res.headersSent) res.writeHead(500); res.end("{}"); }
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  baseURL = `http://127.0.0.1:${server.address().port}`;
  try {
    await sender.goto("about:blank");
    await installSyntheticSender(sender);
    producer = await startPerformanceViewer(viewerPage, { baseURL, nonce, viewerID: "owned-viewer", source: { source_id: "source-a" } });
    await expect.poll(async () => (await producer.sample()).observedDurationMs, { timeout: 12000 }).toBeGreaterThanOrEqual(3000);
    const steady = await producer.sample();
    expect(steady.failure).toBeNull(); expect(steady.decodedFrames).toBeGreaterThan(30); expect(steady.renderedFrames).toBeGreaterThan(30); expect(steady.uniqueDynamicFrames).toBeGreaterThan(30);
    expect(steady.invalidMarkers).toBe(0); expect(steady.staleSourceFrames).toBe(0);
    await producer.switchSource({ source_id: "source-b" }, "switch");
    let switched;
    await expect.poll(async () => { switched = await producer.sample(); return switched.frames.some((frame) => frame.sourceTag === 2); }, { timeout: 10000 }).toBe(true);
    expect(switched.switchFirstDecodedMs).toHaveLength(1); expect(switched.switchFirstDecodedMs[0]).toBeGreaterThan(0);
    const codec = await viewerPage.evaluate(async () => { const video = document.querySelector("video"); return { width: video.videoWidth, height: video.videoHeight }; });
    expect(codec).toEqual({ width: 640, height: 360 });
    expect(requests.filter((r) => r.path === "/clock" && r.method === "GET").length).toBeGreaterThan(0);
    expect(requests.filter((r) => r.path === "/offer" && r.method === "POST")).toHaveLength(2);
    expect(requests.filter((r) => r.method !== "OPTIONS").every((r) => r.authenticated)).toBe(true);
    expect(requests.every((r) => r.origin === "" || r.origin === baseURL)).toBe(true);
    expect(await viewerPage.evaluate(() => location.origin)).toBe(baseURL);
    declaredWidth = 800;
    await producer.switchSource({ source_id: "source-a" }, "switch");
    await expect.poll(async () => (await producer.sample()).failure, { timeout: 10000 }).toBe("decoded_dimensions_mismatch");
    observedEvidence = { scope: "synthetic-headless-only", decodedFrames: steady.decodedFrames, renderedFrames: steady.renderedFrames, uniqueDynamicFrames: steady.uniqueDynamicFrames, observedDurationMs: steady.observedDurationMs, invalidMarkers: steady.invalidMarkers, staleSourceFrames: steady.staleSourceFrames, switchFirstDecodedMs: switched.switchFirstDecodedMs, actualDecodedSize: codec, sizeMismatchRejected: true, requests };
    console.log(`SYNTHETIC ONLY: real headless Chromium H264 decode/rVFC/CRC ${steady.renderedFrames} frames over ${steady.observedDurationMs.toFixed(1)} ms; two valid source offers and explicit negotiated-size mismatch rejection, private fetch auth and same-origin policy verified; no native capture or latency acceptance.`);
  } catch (error) {
    console.log("Owned wire trace:", JSON.stringify(requests));
    if(producer){const last=await producer.sample();console.log("Owned viewer counters:",JSON.stringify({...last,frames:last.frames.length,clockSamples:last.clockSamples.length}));console.log("Owned decoded size:",await viewerPage.evaluate(()=>({width:document.querySelector("video").videoWidth,height:document.querySelector("video").videoHeight})));}
    throw error;
  } finally {
    if (producer) { try { await producer.close(); } catch {} }
    await viewerContext.close(); await producerContext.close(); await browser.close();
    await new Promise((resolve) => server.close(resolve));
  }
  if (process.env.VSCREEN_SYNTHETIC_EVIDENCE) await writeFile(process.env.VSCREEN_SYNTHETIC_EVIDENCE, JSON.stringify({ ...observedEvidence, cleanupConfirmed: true }, null, 2) + "\n", { mode: 0o600 });
}, 35000);
