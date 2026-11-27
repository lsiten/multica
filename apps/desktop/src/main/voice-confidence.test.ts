// @vitest-environment node
import { expect, it } from "vitest";
import { Tensor } from "@huggingface/transformers";
import { VoiceConfidence } from "./voice-confidence";

it("rejects ambiguous decoding instead of returning a plausible sentence", () => {
  const quality = new VoiceConfidence();
  quality._call([], new Tensor("float32", new Float32Array([0, 0, 0, 0]), [1, 4]));
  expect(quality.accepts("明天下午三点开会")).toBe(false);
});

it("rejects repetitive decoding even when the model is confident", () => {
  const quality = new VoiceConfidence();
  quality._call([], new Tensor("float32", new Float32Array([10, 0]), [1, 2]));
  expect(quality.accepts("今天我們要去一集" + "圖".repeat(200))).toBe(false);
  expect(quality.accepts("明天下午三点开会")).toBe(true);
});

it("does not accept text without decoding evidence", () => {
  expect(new VoiceConfidence().accepts("你不想要我")).toBe(false);
});
