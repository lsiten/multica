// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { readReviewFile } from "./local-review-pages";

const api = vi.hoisted(() => ({ supportsPagedLocalMR: vi.fn(), executePagedLocalReview: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api }));
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });
const id = "a".repeat(64);
const request = { task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/repo", target: "main", version_id: id, file_path: "file.txt" };
const response = { version_id: id, path: "file.txt", preview: "text", page: { lines: [], next_line: 0, has_more: false } };

it("keeps owning-runtime file pages local without contacting the server", async () => {
  vi.stubGlobal("daemonAPI", { readLocalReviewPage: vi.fn().mockResolvedValue(response) });
  expect(await readReviewFile(request)).toEqual(response);
  expect(api.supportsPagedLocalMR).not.toHaveBeenCalled();
  expect(api.executePagedLocalReview).not.toHaveBeenCalled();
});
it("uses the relay only after the desktop reports a foreign runtime", async () => {
  vi.stubGlobal("daemonAPI", { readLocalReviewPage: async () => null });
  api.supportsPagedLocalMR.mockResolvedValue(true);
  api.executePagedLocalReview.mockResolvedValue(response);
  expect(await readReviewFile(request)).toEqual(response);
  expect(api.executePagedLocalReview).toHaveBeenCalledWith(expect.objectContaining({ version_id: id, action: "file" }), undefined);
});
it("does not silently relay local failures", async () => {
  vi.stubGlobal("daemonAPI", { readLocalReviewPage: async () => { throw new Error("cache unavailable"); } });
  await expect(readReviewFile(request)).rejects.toThrow("cache unavailable");
  expect(api.executePagedLocalReview).not.toHaveBeenCalled();
});
it("requires paging support instead of falling back to the aggregate diff endpoint", async () => {
  vi.stubGlobal("daemonAPI", {});
  api.supportsPagedLocalMR.mockResolvedValue(false);
  await expect(readReviewFile(request)).rejects.toThrow("local_review_paging_upgrade_required");
  expect(api.executePagedLocalReview).not.toHaveBeenCalled();
});

it("does not dispatch a page whose reader already cancelled", async () => {
  const localRead = vi.fn().mockResolvedValue(response);
  vi.stubGlobal("daemonAPI", { readLocalReviewPage: localRead });
  const controller = new AbortController();
  controller.abort();
  await expect(readReviewFile(request, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  expect(localRead).not.toHaveBeenCalled();
  expect(api.supportsPagedLocalMR).not.toHaveBeenCalled();
});

it("passes reader cancellation through capability discovery and relay reads", async () => {
  vi.stubGlobal("daemonAPI", {});
  const controller = new AbortController();
  api.supportsPagedLocalMR.mockResolvedValue(true);
  api.executePagedLocalReview.mockResolvedValue(response);
  await readReviewFile(request, controller.signal);
  expect(api.supportsPagedLocalMR).toHaveBeenCalledWith(controller.signal);
  expect(api.executePagedLocalReview).toHaveBeenCalledWith(expect.objectContaining({ action: "file" }), controller.signal);
});

it("never reroutes a cancelled local discovery response to the cloud", async () => {
  const controller = new AbortController();
  vi.stubGlobal("daemonAPI", { readLocalReviewPage: async () => {
    controller.abort();
    return null;
  } });
  await expect(readReviewFile(request, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  expect(api.supportsPagedLocalMR).not.toHaveBeenCalled();
  expect(api.executePagedLocalReview).not.toHaveBeenCalled();
});

it("does not start a relay read after capability discovery was cancelled", async () => {
  vi.stubGlobal("daemonAPI", {});
  const controller = new AbortController();
  api.supportsPagedLocalMR.mockImplementationOnce(async () => {
    controller.abort();
    return true;
  });
  await expect(readReviewFile(request, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  expect(api.executePagedLocalReview).not.toHaveBeenCalled();
});

it("sends the same IPC read identifier on cancellation and removes the listener", async () => {
  const controller = new AbortController();
  const cancelRead = vi.fn();
  let invokedID: unknown;
  vi.stubGlobal("daemonAPI", {
    cancelLocalReviewRead: cancelRead,
    readLocalReviewPage: async (_request: unknown, readID: unknown) => {
      invokedID = readID;
      controller.abort();
      return response;
    },
  });
  const removeListener = vi.spyOn(controller.signal, "removeEventListener");
  await expect(readReviewFile(request, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  expect(typeof invokedID).toBe("string");
  expect(cancelRead).toHaveBeenCalledExactlyOnceWith(invokedID);
  expect(removeListener).toHaveBeenCalledWith("abort", expect.any(Function));
});
