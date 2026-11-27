import { deflateSync } from "node:zlib";
import { LogitsProcessor, type Tensor } from "@huggingface/transformers";

export class VoiceConfidence extends LogitsProcessor {
  private totalLogProbability = 0;
  private tokens = 0;

  _call(_inputIds: bigint[][], logits: Tensor): Tensor {
    let maximum = -Infinity;
    for (const value of logits.data) maximum = Math.max(maximum, Number(value));
    let denominator = 0;
    for (const value of logits.data) denominator += Math.exp(Number(value) - maximum);
    this.totalLogProbability -= Math.log(denominator);
    this.tokens += 1;
    return logits;
  }

  accepts(text: string): boolean {
    // Match Whisper's rejection thresholds for uncertain or repetitive decoding.
    const bytes = Buffer.from(text.trim(), "utf8");
    return bytes.length > 0 && bytes.length <= 16 * 1024
      && this.tokens > 0 && this.totalLogProbability / this.tokens >= -1
      && bytes.length / deflateSync(bytes).length <= 2.4;
  }
}
