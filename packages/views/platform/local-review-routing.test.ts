// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { readLocalReview } from "./local-review";

const api = vi.hoisted(() => ({ supportsLocalMR: vi.fn(), executeLocalReview: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api }));
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });

const request = { task_id: "task", workspace_id: "workspace", runtime_id: "local-runtime", path: "/repo", target: "main" };
const snapshot = { id: "id", path: "/repo", branch: "feature", target: "main", head: "head", target_head: "base", base: "base", dirty: false, branches: [], commits: "", files: [], review: { snapshot_id: "id", state: "draft", comment: "", merged_commit: "" } };

it("does not contact the backend when desktop resolves the owning runtime locally", async () => {
  const local = vi.fn().mockResolvedValue(snapshot);
  vi.stubGlobal("window", { daemonAPI: { readLocalReview: local } });
  await expect(readLocalReview(request)).resolves.toMatchObject({ id: "id", review: { state: "draft" } });
  expect(local).toHaveBeenCalledWith(request);
  expect(api.supportsLocalMR).not.toHaveBeenCalled();
  expect(api.executeLocalReview).not.toHaveBeenCalled();
});

it.each(["submit", "approve", "request_changes", "merge"] as const)("rejects retired %s before local or relay dispatch", async (action) => {
  const local = vi.fn();
  vi.stubGlobal("window", { daemonAPI: { readLocalReview: local } });
  await expect(readLocalReview({ ...request, action })).rejects.toThrow("local_review_workflow_retired");
  expect(local).not.toHaveBeenCalled();
  expect(api.supportsLocalMR).not.toHaveBeenCalled();
  expect(api.executeLocalReview).not.toHaveBeenCalled();
});

it("uses the relay when desktop confirms the runtime is not local", async () => {
  vi.stubGlobal("window", { daemonAPI: { readLocalReview: vi.fn().mockResolvedValue(null) } });
  api.supportsLocalMR.mockResolvedValue(true);
  api.executeLocalReview.mockResolvedValue(snapshot);
  await readLocalReview(request);
  expect(api.executeLocalReview).toHaveBeenCalledWith(request);
});

it("surfaces local errors without silently rerouting the request", async () => {
  vi.stubGlobal("window", { daemonAPI: { readLocalReview: vi.fn().mockRejectedValue(new Error("local repository unavailable")) } });
  await expect(readLocalReview(request)).rejects.toThrow("local repository unavailable");
  expect(api.executeLocalReview).not.toHaveBeenCalled();
});
