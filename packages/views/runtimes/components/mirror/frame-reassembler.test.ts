// @vitest-environment node

import { describe, expect, it } from "vitest";
import { MirrorFrameReassembler } from "./frame-reassembler";

function header(id: number, chunks: number, width = 1280, height = 720) {
  const packet = new Uint8Array(15);
  const view = new DataView(packet.buffer);
  packet[0] = 1;
  view.setUint32(1, id);
  view.setUint16(5, chunks);
  view.setUint32(7, width);
  view.setUint32(11, height);
  return packet;
}

function chunk(id: number, index: number, bytes: number[]) {
  const packet = new Uint8Array(7 + bytes.length);
  const view = new DataView(packet.buffer);
  packet[0] = 2;
  view.setUint32(1, id);
  view.setUint16(5, index);
  packet.set(bytes, 7);
  return packet;
}

describe("MirrorFrameReassembler", () => {
  it("emits a complete one-chunk frame", () => {
    const reassembler = new MirrorFrameReassembler();

    expect(reassembler.push(header(1, 1))).toBeNull();
    expect(reassembler.push(chunk(1, 0, [1, 2, 3]))).toEqual({
      id: 1,
      width: 1280,
      height: 720,
      jpeg: new Uint8Array([1, 2, 3]),
    });
  });

  it("waits for every chunk and ignores malformed packets", () => {
    const reassembler = new MirrorFrameReassembler();

    expect(reassembler.push(new Uint8Array([9]))).toBeNull();
    expect(reassembler.push(chunk(2, 0, [1]))).toBeNull();
    expect(reassembler.push(header(2, 2))).toBeNull();
    expect(reassembler.push(chunk(2, 2, [9]))).toBeNull();
    expect(reassembler.push(chunk(2, 0, [1]))).toBeNull();
    expect(reassembler.push(chunk(2, 0, [8]))).toBeNull();
    expect(reassembler.push(chunk(2, 1, [2]))).toEqual({
      id: 2,
      width: 1280,
      height: 720,
      jpeg: new Uint8Array([1, 2]),
    });
  });

  it("does not replace a complete newer frame with an older frame", () => {
    const reassembler = new MirrorFrameReassembler();

    reassembler.push(header(1, 2));
    reassembler.push(header(2, 1));
    expect(reassembler.push(chunk(2, 0, [2]))?.id).toBe(2);
    expect(reassembler.push(chunk(1, 0, [1]))).toBeNull();
    expect(reassembler.push(chunk(1, 1, [1]))).toBeNull();
  });
});
