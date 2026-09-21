// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());
describe("chat voice transcription", () => {
  it("sends bounded audio to the selected agent without creating a chat or mirror", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ text: " recognized " })));
    vi.stubGlobal("fetch", fetch);
    const result = await new ApiClient("https://example.test").transcribeChatVoice("agent", new Blob(["a"], { type: "audio/webm" }), new AbortController().signal);
    expect(result).toBe("recognized");
    expect(fetch).toHaveBeenCalledOnce();
    expect(fetch.mock.calls[0]?.[0]).toBe("https://example.test/api/agents/agent/voice/transcribe");
    expect(JSON.parse(fetch.mock.calls[0]?.[1].body)).toEqual({ mime_type: "audio/webm", audio_base64: "YQ==" });
  });
  it.each([{}, { text: "" }, { text: 42 }, { text: "a".repeat(16385) }])("rejects malformed transcripts %j", async (response) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(response))));
    await expect(new ApiClient("https://example.test").transcribeChatVoice("agent", new Blob(["a"]), new AbortController().signal)).rejects.toThrow("invalid_voice_transcript");
  });
  it("does not upload cancelled or oversized audio", async () => {
    const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
    const controller = new AbortController(); controller.abort();
    const api = new ApiClient("https://example.test");
    await expect(api.transcribeChatVoice("agent", new Blob(["a"]), controller.signal)).rejects.toThrow();
    await expect(api.transcribeChatVoice("agent", new Blob([new Uint8Array(512 * 1024 + 1)]), new AbortController().signal)).rejects.toThrow("invalid_audio");
    expect(fetch).not.toHaveBeenCalled();
  });
});
