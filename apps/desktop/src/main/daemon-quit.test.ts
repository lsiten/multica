// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { createDaemonQuitHandler } from "./daemon-quit";

afterEach(() => { vi.useRealTimers(); });

it("blocks exit synchronously until local stop confirms, including duplicate exit events", async () => {
  let loaded!: (value: boolean) => void;
  let stopped!: () => void;
  const quit = vi.fn();
  const stop = vi.fn(() => new Promise<void>((resolve) => { stopped = resolve; }));
  const handler = createDaemonQuitHandler({ prepare: vi.fn(), autoStop: () => new Promise((resolve) => { loaded = resolve; }), stop, quit });
  const event = { preventDefault: vi.fn() };
  handler(event);
  handler(event);
  expect(event.preventDefault).toHaveBeenCalledTimes(2);
  expect(quit).not.toHaveBeenCalled();
  loaded(true);
  await Promise.resolve();
  expect(stop).toHaveBeenCalledOnce();
  expect(quit).not.toHaveBeenCalled();
  stopped();
  await Promise.resolve();
  expect(quit).toHaveBeenCalledOnce();
  handler(event);
  expect(event.preventDefault).toHaveBeenCalledTimes(2);
});

it("leaves the local runtime running when auto-stop is disabled", async () => {
  const quit = vi.fn(), stop = vi.fn();
  const handler = createDaemonQuitHandler({ prepare: vi.fn(), autoStop: async () => false, stop, quit });
  handler({ preventDefault: vi.fn() });
  await Promise.resolve();
  expect(stop).not.toHaveBeenCalled();
  expect(quit).toHaveBeenCalledOnce();
});

it("bounds a stalled local stop without waiting for remote synchronization", async () => {
  vi.useFakeTimers();
  const quit = vi.fn();
  const handler = createDaemonQuitHandler({ prepare: vi.fn(), autoStop: async () => true, stop: () => new Promise(() => {}), quit });
  handler({ preventDefault: vi.fn() });
  await vi.advanceTimersByTimeAsync(15_000);
  expect(quit).toHaveBeenCalledOnce();
});

it("releases exit when preferences or local stop fail", async () => {
  const quit = vi.fn();
  createDaemonQuitHandler({ prepare: vi.fn(), autoStop: async () => { throw new Error("unreadable preferences"); }, stop: vi.fn(), quit })({ preventDefault: vi.fn() });
  await Promise.resolve();
  expect(quit).toHaveBeenCalledOnce();
});
