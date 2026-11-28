import { Converter } from "opencc-js";

let simplified: ((text: string) => string) | undefined;
let traditional: ((text: string) => string) | undefined;

/** Match the chosen UI script without translating or correcting recognized words. */
export function normalizeVoiceTranscript(text: string, locale: string): string {
  switch (locale) {
    case "zh-Hans":
      simplified ??= Converter({ from: "t", to: "cn" });
      return simplified(text);
    case "zh-Hant":
      traditional ??= Converter({ from: "cn", to: "tw" });
      return traditional(text);
    default:
      return text;
  }
}
