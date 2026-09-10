// @vitest-environment node
import { expect, it, vi } from "vitest";
import { probeReviewRepositories } from "./local-review-probe";
import { readReviewRepositories } from "./local-review-pages";

vi.mock("./local-review-pages", () => ({ readReviewRepositories: vi.fn() }));
it("bounds probes and never sends a queued request after cancellation", async () => {
  const releases: Array<() => void> = [];
  vi.mocked(readReviewRepositories).mockImplementation(() => new Promise((resolve) => releases.push(() => resolve({ repositories: [] }))));
  const input = { task_id: "run", workspace_id: "ws", path: "/repo", target: "main" };
  const running = [0, 1, 2].map(() => probeReviewRepositories(input, new AbortController().signal));
  const canceled = new AbortController();
  const queued = probeReviewRepositories(input, canceled.signal);
  const rejected = expect(queued).rejects.toMatchObject({ name: "AbortError" });
  expect(readReviewRepositories).toHaveBeenCalledTimes(3);
  canceled.abort();
  await rejected;
  for (const release of releases) release();
  await Promise.all(running);
  expect(readReviewRepositories).toHaveBeenCalledTimes(3);
  vi.mocked(readReviewRepositories).mockResolvedValue({ repositories: ["/repo"] });
  expect(await probeReviewRepositories(input, new AbortController().signal)).toEqual({ repositories: ["/repo"] });
});
