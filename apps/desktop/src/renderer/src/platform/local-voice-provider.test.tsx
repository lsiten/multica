import { describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { useLocalVoice } from "@multica/views/platform";
import {
  LocalVoiceProvider,
  buildLocalVoiceAdapter,
  mapVoiceLanguage,
} from "./local-voice-provider";
import type { LocalVoiceStatus } from "@multica/core/platform";
import type { LocalVoiceAPI, LocalVoiceLanguage } from "../../../shared/local-voice";

function makeApi(overrides: Partial<LocalVoiceAPI> = {}): LocalVoiceAPI & { transcribe: ReturnType<typeof vi.fn> } {
  const listeners: Array<(status: LocalVoiceStatus) => void> = [];
  return {
    getStatus: vi.fn(async () => ({ phase: "ready" })),
    onStatus: vi.fn((listener: (status: LocalVoiceStatus) => void) => {
      listeners.push(listener);
      return () => {
        const at = listeners.indexOf(listener);
        if (at >= 0) listeners.splice(at, 1);
      };
    }),
    retry: vi.fn(async () => {}),
    transcribe: vi.fn(async (_id: string, _samples: Float32Array, _language: LocalVoiceLanguage) => "ok"),
    cancel: vi.fn(() => {}),
    ...overrides,
  } as unknown as (LocalVoiceAPI & { transcribe: ReturnType<typeof vi.fn> });
}

describe("mapVoiceLanguage", () => {
  it("maps supported UI locales to the worker languages", () => {
    expect(mapVoiceLanguage("en")).toBe("en");
    expect(mapVoiceLanguage("zh-Hans")).toBe("zh");
    expect(mapVoiceLanguage("zh-Hant")).toBe("zh");
    expect(mapVoiceLanguage("ja")).toBe("ja");
    expect(mapVoiceLanguage("ko")).toBe("ko");
    expect(mapVoiceLanguage("fr")).toBe("fr");
  });
  it("falls back to English for unsupported locales", () => {
    expect(mapVoiceLanguage("de")).toBe("en");
    expect(mapVoiceLanguage("")).toBe("en");
  });
});

describe("buildLocalVoiceAdapter", () => {
  it("mirrors status changes through the shared subscribe contract", async () => {
    const api = makeApi();
    const adapter = buildLocalVoiceAdapter(api, () => "en");
    let seen: unknown;
    adapter.subscribe(() => {
      seen = adapter.getSnapshot();
    });
    // The initial getStatus warms the snapshot.
    await vi.waitFor(() => expect(seen).toEqual({ phase: "ready" }));
  });

  it("retries through the preload API", async () => {
    const api = makeApi();
    buildLocalVoiceAdapter(api, () => "en").retry();
    expect(api.retry).toHaveBeenCalledOnce();
  });

  it("decodes the recording to 16 kHz PCM and forwards language, cancelling on abort", async () => {
    class FakeAudioContext {
      async decodeAudioData() {
        return { duration: 1, sampleRate: 48_000 };
      }
      close() {}
    }
    class FakeOfflineAudioContext {
      destination = {};
      createBufferSource() {
        return { buffer: undefined as never, connect() {}, start() {} };
      }
      async startRendering() {
        return { getChannelData: () => new Float32Array([0.1, -0.2]) };
      }
    }
    vi.stubGlobal("AudioContext", FakeAudioContext as never);
    vi.stubGlobal("OfflineAudioContext", FakeOfflineAudioContext as never);
    try {
      const api = makeApi();
      const adapter = buildLocalVoiceAdapter(api, () => "zh");
      const text = await adapter.transcribe(new Blob(["x"], { type: "audio/webm" }), new AbortController().signal);
      expect(text).toBe("ok");
      expect(api.transcribe).toHaveBeenCalledOnce();
      const call = api.transcribe.mock.calls[0];
      expect(call[2]).toBe("zh");
      expect(call[1]).toBeInstanceOf(Float32Array);
      expect((call[1] as Float32Array).length).toBe(2);
      // Aborting the recording cancels the in-flight worker job.
      const controller = new AbortController();
      const pending = adapter.transcribe(new Blob(["x"], { type: "audio/webm" }), controller.signal);
      controller.abort();
      await pending.catch(() => {});
      expect(api.cancel).toHaveBeenCalledOnce();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe("LocalVoiceProvider", () => {
  it("provides an adapter when the preload exposes localVoiceAPI", () => {
    const api = makeApi();
    (globalThis as never as { window: { localVoiceAPI?: LocalVoiceAPI } }).window.localVoiceAPI = api;
    const Probe = () => {
      const { adapter } = useLocalVoice();
      return <span data-testid="present">{adapter != null ? "yes" : "no"}</span>;
    };
    const { getByTestId } = render(
      <LocalVoiceProvider locale="en"><Probe /></LocalVoiceProvider>,
    );
    expect(getByTestId("present").textContent).toBe("yes");
    delete (globalThis as never as { window: { localVoiceAPI?: LocalVoiceAPI } }).window.localVoiceAPI;
  });

  it("keeps the context null without a preload API", () => {
    (globalThis as never as { window: { localVoiceAPI?: LocalVoiceAPI } }).window.localVoiceAPI = undefined;
    let captured: unknown = "unset";
    const Probe = () => {
      captured = useLocalVoice().adapter;
      return null;
    };
    render(
      <LocalVoiceProvider locale="en">
        <Probe />
      </LocalVoiceProvider>,
    );
    expect(captured).toBeNull();
  });
});
