import { randomBytes } from "node:crypto";

export const REMOTE_MAX_BYTES = 1024 * 1024;
// One bounded, bidirectional channel; only the caller's named handlers exist.
export function remoteChannel(input, output, handlers, { timeoutMs = 15000, onClose = () => {} } = {}) {
  const pending = new Map(); let buffer = Buffer.alloc(0), closed = false, active = 0;
  const prefix = randomBytes(8).toString("hex"); let sequence = 0;
  const close = () => { if (closed) return; closed = true; for (const item of pending.values()) { clearTimeout(item.timer); item.reject(new Error("remote_channel_closed")); } pending.clear(); input.off("data", data); onClose(); };
  const send = (value) => { const bytes = Buffer.from(JSON.stringify(value) + "\n"); if (closed || bytes.length > REMOTE_MAX_BYTES || output.writableLength > REMOTE_MAX_BYTES * 2) { close(); throw new Error("remote_channel_capacity"); } output.write(bytes); };
  const processLine = async (line) => {
    let value; try { value = JSON.parse(line); } catch { close(); return; }
    if (!value || value.v !== 1 || typeof value.id !== "string" || value.id.length > 80) { close(); return; }
    if (value.type === "reply") { const item = pending.get(value.id); if (!item) { close(); return; } pending.delete(value.id); clearTimeout(item.timer); if (value.ok === true) item.resolve(value.result); else item.reject(new Error("remote_request_refused")); return; }
    if (value.type !== "request" || !Object.hasOwn(handlers, value.method) || typeof handlers[value.method] !== "function" || active >= 8) { close(); return; }
    active++;
    try { const result = await handlers[value.method](value.body); send({ v: 1, type: "reply", id: value.id, ok: true, result }); }
    catch { if (!closed) send({ v: 1, type: "reply", id: value.id, ok: false }); }
    finally { active--; }
  };
  const data = (chunk) => {
    buffer = Buffer.concat([buffer, Buffer.from(chunk)]);
    if (buffer.length > REMOTE_MAX_BYTES) { close(); return; }
    for (;;) { const end = buffer.indexOf(10); if (end < 0) break; const line = buffer.subarray(0, end).toString(); buffer = buffer.subarray(end + 1); void processLine(line).catch(close); }
  };
  input.on("data", data); input.once("end", close); input.once("error", close); output.once("error", close);
  return { close, get closed() { return closed; }, request(method, body, requestTimeoutMs = timeoutMs) { if (!Number.isInteger(requestTimeoutMs) || requestTimeoutMs < 1 || requestTimeoutMs > 45000) return Promise.reject(new Error("remote_timeout_invalid")); if (closed || pending.size >= 16) return Promise.reject(new Error("remote_channel_capacity")); return new Promise((resolve, reject) => { const id = `${prefix}-${++sequence}`; const timer = setTimeout(() => { pending.delete(id); reject(new Error("remote_request_timeout")); close(); }, requestTimeoutMs); pending.set(id, { resolve, reject, timer }); try { send({ v: 1, type: "request", id, method, body }); } catch (error) { clearTimeout(timer); pending.delete(id); reject(error); } }); } };
}
