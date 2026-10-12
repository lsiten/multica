// @vitest-environment node
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(AbortSignal, "timeout").mockImplementation((ms) => {
    const controller = new AbortController();
    setTimeout(() => controller.abort(new DOMException("Request timed out", "TimeoutError")), ms);
    return controller.signal;
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("bounds an identity request without retrying it on the wire", async () => {
  const fetch = vi.fn((_url: string, init: RequestInit) => new Promise<Response>((_resolve, reject) => {
    init.signal?.addEventListener("abort", () => reject(init.signal?.reason), { once: true });
  }));
  vi.stubGlobal("fetch", fetch);
  const client = new ApiClient("https://fixture.invalid");
  const pending = client.getMe();
  const rejected = expect(pending).rejects.toMatchObject({ name: "TimeoutError" });
  await vi.advanceTimersByTimeAsync(30_000);
  await rejected;
  expect(fetch).toHaveBeenCalledOnce();
});

it("keeps the deadline through a stalled response body", async () => {
  vi.stubGlobal("fetch", vi.fn(async (_url: string, init: RequestInit) => new Response(
    new ReadableStream({
      start(controller) {
        init.signal?.addEventListener("abort", () => controller.error(init.signal?.reason), { once: true });
      },
    }),
    { status: 200 },
  )));
  const client = new ApiClient("https://fixture.invalid");
  const rejected = expect(client.getMe()).rejects.toMatchObject({ name: "TimeoutError" });
  await vi.advanceTimersByTimeAsync(30_000);
  await rejected;
});

it("preserves caller cancellation", async () => {
  vi.stubGlobal("fetch", vi.fn((_url: string, init: RequestInit) => new Promise<Response>((_resolve, reject) => {
    if (init.signal?.aborted) reject(init.signal.reason);
    else init.signal?.addEventListener("abort", () => reject(init.signal?.reason), { once: true });
  })));
  const caller = new AbortController();
  const client = new ApiClient("https://fixture.invalid");
  const rejected = expect(client.listApplications("workspace", undefined, caller.signal)).rejects.toThrow("cancelled by caller");
  caller.abort(new Error("cancelled by caller"));
  await rejected;
});
