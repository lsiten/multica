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

it("keeps local approvals on the local daemon without backend capability checks", async () => {
  const local = vi.fn().mockResolvedValue({ ...snapshot, review: { ...snapshot.review, state: "approved" } });
  vi.stubGlobal("window", { daemonAPI: { readLocalReview: local } });
  const approval = { ...request, action: "approve", snapshot_id: "id", command_id: "approval-1" } as const;
  await expect(readLocalReview(approval)).resolves.toMatchObject({ review: { state: "approved" } });
  expect(local).toHaveBeenCalledWith(approval);
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

it("generates secure operation IDs when randomUUID is unavailable on HTTP", async () => {
  vi.stubGlobal("window", {});
  vi.stubGlobal("crypto", { getRandomValues: (bytes: Uint8Array) => {
    for (let index = 0; index < bytes.length; index++) bytes[index] = index;
    return bytes;
  } });
  api.supportsLocalMR.mockResolvedValue(true);
  api.executeLocalReview.mockResolvedValue(snapshot);
  await readLocalReview({ ...request, action: "approve", snapshot_id: "id" });
  expect(api.executeLocalReview).toHaveBeenCalledWith(expect.objectContaining({ command_id: "00010203-0405-4607-8809-0a0b0c0d0e0f" }));
});

it("does not dispatch a mutation when secure randomness is unavailable", async () => {
  vi.stubGlobal("crypto", {});
  await expect(readLocalReview({ ...request, action: "approve", snapshot_id: "id" })).rejects.toThrow("Secure UUID generation requires crypto.getRandomValues");
  expect(api.executeLocalReview).not.toHaveBeenCalled();
});
