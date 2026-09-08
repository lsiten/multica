// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { readLocalReview } from "./local-review";

const api = vi.hoisted(() => ({ supportsLocalMR: vi.fn(), createLocalMR: vi.fn(), executeLocalReview: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api }));

describe("local MR server capability", () => {
  it("rejects an old backend before issuing a create request", async () => {
    api.supportsLocalMR.mockResolvedValue(false);
    await expect(readLocalReview({ task_id: "task", workspace_id: "ws", path: "/runtime/repo", target: "main" })).rejects.toThrow("local_review_upgrade_required");
    expect(api.createLocalMR).not.toHaveBeenCalled();
  });

  it("reads the runtime response without creating a cloud MR", async () => {
    api.supportsLocalMR.mockResolvedValue(true);
    const snapshot = { id: "snapshot", review: { state: "draft", events: [] } };
    api.executeLocalReview.mockResolvedValue(snapshot);
    const request = { task_id: "task", workspace_id: "ws", path: "/runtime/repo", target: "main" };
    await expect(readLocalReview(request)).resolves.toMatchObject(snapshot);
    expect(api.executeLocalReview).toHaveBeenCalledWith(request);
    expect(api.createLocalMR).not.toHaveBeenCalled();
  });
});
