// @vitest-environment node
import { EventEmitter } from "node:events";
import { expect, it } from "vitest";
import { LocalReviewCancellation } from "./local-review-cancellation";

it("only cancels the matching window's read and removes completed registrations", async () => {
  const registry = new LocalReviewCancellation();
  const first = new EventEmitter();
  const second = new EventEmitter();
  let active: AbortSignal | undefined;
  const reading = registry.run(first, "read-1", async (signal) => {
    active = signal;
    await new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve(), { once: true }));
  });
  registry.cancel(second, "read-1");
  expect(active?.aborted).toBe(false);
  registry.cancel(first, "read-1");
  await reading;
  expect(active?.aborted).toBe(true);
  expect(first.listenerCount("destroyed")).toBe(0);
});

it("cancels outstanding reads when their window is destroyed", async () => {
  const registry = new LocalReviewCancellation();
  const owner = new EventEmitter();
  const reading = registry.run(owner, "read-2", async (signal) => {
    await new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve(), { once: true }));
    return signal.aborted;
  });
  owner.emit("destroyed");
  expect(await reading).toBe(true);
  expect(owner.listenerCount("destroyed")).toBe(0);
});
