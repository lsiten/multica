import type { LocalReviewRequest } from "@multica/core/types/local-review";
import { readReviewRepositories } from "./local-review-pages";

let active = 0;
const waiting: Array<() => void> = [];

// Inventory pages can contain many historical runs; bound runtime relay traffic.
export async function probeReviewRepositories(request: LocalReviewRequest, signal: AbortSignal) {
  signal.throwIfAborted();
  if (active >= 3) {
    await new Promise<void>((resolve, reject) => {
      const start = () => { signal.removeEventListener("abort", cancel); active++; resolve(); };
      const cancel = () => { const index = waiting.indexOf(start); if (index >= 0) waiting.splice(index, 1); reject(signal.reason); };
      waiting.push(start);
      signal.addEventListener("abort", cancel, { once: true });
    });
  } else active++;
  try {
    signal.throwIfAborted();
    return await readReviewRepositories({ ...request, target: "", action: "repositories" }, signal);
  } finally {
    active--;
    waiting.shift()?.();
  }
}
