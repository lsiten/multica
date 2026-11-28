// @vitest-environment node
import { expect, it } from "vitest";
import { normalizeVoiceTranscript } from "./transcript-locale";

it.each([
  ["zh-Hans", "今天我們測試語音輸入，下午3點開會。", "今天我们测试语音输入，下午3点开会。"],
  ["zh-Hant", "今天我们测试语音输入，下午3点开会。", "今天我們測試語音輸入，下午3點開會。"],
  ["en", "Meeting at 3 PM. 今天測試", "Meeting at 3 PM. 今天測試"],
  ["ja", "音声入力のテスト", "音声入力のテスト"],
  ["zh-Hans", "", ""],
  ["zh-Hans", "語音書入 123 ABC", "语音书入 123 ABC"],
])("uses locale %s for transcript script without rewriting recognition", (locale, text, expected) => {
  expect(normalizeVoiceTranscript(text, locale)).toBe(expected);
});
