// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { supportsSelectedMerge } from "./local-review-selection";
const api = vi.hoisted(() => ({ supportsSelectedMerge: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api }));
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });
const request = { task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/repo", target: "main" };
it.each([true, false])("uses the owning local runtime capability without contacting cloud", async (supported) => {
  vi.stubGlobal("daemonAPI", { reviewInventory: async () => ({ health: { profile: "desktop", workspaces: [{ id: "ws", runtimes: ["runtime"] }], local_review_selected_merge_supported: supported } }) });
  expect(await supportsSelectedMerge(request)).toBe(supported);
  expect(api.supportsSelectedMerge).not.toHaveBeenCalled();
});
it("checks remote capability for a foreign runtime", async () => {
  vi.stubGlobal("daemonAPI", { reviewInventory: async () => ({ health: { profile: "desktop", workspaces: [] } }) });
  api.supportsSelectedMerge.mockResolvedValue(true);
  expect(await supportsSelectedMerge(request)).toBe(true);
});
