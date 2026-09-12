export interface MirrorFrame {
  id: number;
  width: number;
  height: number;
  jpeg: Uint8Array;
}

interface PendingFrame {
  width: number;
  height: number;
  chunks: Array<Uint8Array | undefined>;
  remaining: number;
}

const HEADER_SIZE = 15;
const CHUNK_HEADER_SIZE = 7;

export class MirrorFrameReassembler {
  private pending = new Map<number, PendingFrame>();
  private latestComplete = 0;

  push(packet: ArrayBuffer | Uint8Array): MirrorFrame | null {
    const bytes = packet instanceof Uint8Array ? packet : new Uint8Array(packet);
    if (bytes.length === 0) return null;
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    if (bytes[0] === 1) return this.pushHeader(view, bytes);
    if (bytes[0] === 2) return this.pushChunk(view, bytes);
    return null;
  }

  private pushHeader(view: DataView, bytes: Uint8Array): MirrorFrame | null {
    if (bytes.length < HEADER_SIZE) return null;
    const id = view.getUint32(1);
    const chunkCount = view.getUint16(5);
    const width = view.getUint32(7);
    const height = view.getUint32(11);
    if (id <= this.latestComplete || chunkCount === 0 || width === 0 || height === 0) return null;
    this.pending.set(id, {
      width,
      height,
      chunks: Array.from({ length: chunkCount }),
      remaining: chunkCount,
    });
    return null;
  }

  private pushChunk(view: DataView, bytes: Uint8Array): MirrorFrame | null {
    if (bytes.length <= CHUNK_HEADER_SIZE) return null;
    const id = view.getUint32(1);
    const index = view.getUint16(5);
    const pending = this.pending.get(id);
    if (!pending || index >= pending.chunks.length || pending.chunks[index]) return null;
    pending.chunks[index] = bytes.slice(CHUNK_HEADER_SIZE);
    pending.remaining -= 1;
    if (pending.remaining !== 0 || id <= this.latestComplete) return null;
    const total = pending.chunks.reduce((sum, chunk) => sum + (chunk?.length ?? 0), 0);
    const jpeg = new Uint8Array(total);
    let offset = 0;
    for (const chunk of pending.chunks) {
      if (!chunk) return null;
      jpeg.set(chunk, offset);
      offset += chunk.length;
    }
    this.latestComplete = id;
    this.pending.forEach((_value, frameId) => {
      if (frameId <= id) this.pending.delete(frameId);
    });
    return { id, width: pending.width, height: pending.height, jpeg };
  }
}
