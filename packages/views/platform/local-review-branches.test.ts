import { afterEach, expect, it, vi } from "vitest";
import { readLocalReviewBranches } from "./local-review";
const api = vi.hoisted(() => ({ supportsLocalMR: vi.fn(), listLocalReviewBranches: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api }));
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });
const request = { task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/repo", target: "missing" };

it("reads branches locally without requiring a valid target snapshot", async () => {
  vi.stubGlobal("daemonAPI", { readLocalReviewBranches: async () => ({ branches: ["test", "release"] }) });
  expect(await readLocalReviewBranches(request)).toEqual(["test", "release"]);
  expect(api.listLocalReviewBranches).not.toHaveBeenCalled();
});
it("relays branches when the runtime belongs to another machine", async () => {
  vi.stubGlobal("daemonAPI", { readLocalReviewBranches: async () => null });
  api.supportsLocalMR.mockResolvedValue(true);
  api.listLocalReviewBranches.mockResolvedValue(["test"]);
  expect(await readLocalReviewBranches(request)).toEqual(["test"]);
  expect(api.listLocalReviewBranches).toHaveBeenCalledWith(request, undefined);
});
it("rejects malformed local branch payloads", async () => {
  vi.stubGlobal("daemonAPI", { readLocalReviewBranches: async () => ({ branches: [42] }) });
  await expect(readLocalReviewBranches(request)).rejects.toThrow();
});

it("cancels the local branch request without falling back to the relay", async () => {
  const controller = new AbortController();
  const cancelRead = vi.fn();
  let identifier: unknown;
  vi.stubGlobal("daemonAPI", {
    cancelLocalReviewRead: cancelRead,
    readLocalReviewBranches: async (_input: unknown, id: unknown) => {
      identifier = id;
      controller.abort();
      return null;
    },
  });
  await expect(readLocalReviewBranches(request, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  expect(typeof identifier).toBe("string");
  expect(cancelRead).toHaveBeenCalledExactlyOnceWith(identifier);
  expect(api.listLocalReviewBranches).not.toHaveBeenCalled();
});
