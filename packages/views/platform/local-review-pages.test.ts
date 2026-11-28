// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { readReviewFile, requestReviewPage } from "./local-review-pages";

const api = vi.hoisted(() => ({ getConfig: vi.fn(), supportsPagedLocalMR: vi.fn(), executePagedLocalReview: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api }));
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });
const id = "a".repeat(64);
const request = { task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/repo", target: "main", version_id: id, file_path: "file.txt" };
const response = { version_id: id, path: "file.txt", preview: "text", page: { lines: [], next_line: 0, has_more: false } };

it("refuses file writes when the relay server has not declared operation support", async () => {
  vi.stubGlobal("daemonAPI", {});
  api.getConfig.mockResolvedValue({});
  await expect(requestReviewPage({ task_id: "task", workspace_id: "ws", path: "/root", action: "file_operation", file_operation: { kind: "create_file", path: "new.md" }, command_id: "create-1" })).rejects.toThrow("workspace_file_operations_upgrade_required");
  expect(api.executePagedLocalReview).not.toHaveBeenCalled();
});

it("requires the caller to retain a stable file-operation ID before dispatch", async () => {
  await expect(requestReviewPage({ task_id: "task", workspace_id: "ws", path: "/root", action: "file_operation", file_operation: { kind: "trash", path: "old.md" } })).rejects.toThrow(/operation identity/);
  expect(api.getConfig).not.toHaveBeenCalled();
  expect(api.executePagedLocalReview).not.toHaveBeenCalled();
});

it.each([undefined, false, "yes", true])("requires server selection checks before moving a selected file: %s", async (supported) => {
  const expected = "26d9d04d-5c45-40b6-bb58-c60e7d59433c";
  const selected = { task_id: "task", workspace_id: "ws", path: "/root", action: "file_operation", command_id: "move-selected", file_operation: { kind: "move", path: "old.md", destination: "new.md", expected_file_id: expected } } as const;
  const receipt = { kind: "file_operation", command_id: "move-selected", operation: "move", path: "old.md", destination: "new.md", file_id: expected, directory: false };
  api.getConfig.mockResolvedValue({ workspace_file_operations_supported: true, workspace_file_selection_supported: supported });
  api.executePagedLocalReview.mockResolvedValue(receipt);

  const operation = requestReviewPage(selected);

  if (supported === true) await expect(operation).resolves.toEqual(receipt);
  else {
    await expect(operation).rejects.toThrow("workspace_file_operations_upgrade_required");
    expect(api.executePagedLocalReview).not.toHaveBeenCalled();
  }
});

it("authorizes filesystem mutations through the server even for the owning desktop runtime", async () => {
  const localRead = vi.fn();
  vi.stubGlobal("daemonAPI", { readLocalReviewPage: localRead });
  api.getConfig.mockResolvedValue({ workspace_file_operations_supported: true });
  const receipt = { kind: "file_operation", command_id: "create-1", operation: "create_file", path: "new.md", directory: false };
  api.executePagedLocalReview.mockResolvedValue(receipt);
  expect(await requestReviewPage({ task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/root", action: "file_operation", command_id: "create-1", file_operation: { kind: "create_file", path: "new.md" } })).toEqual(receipt);
  expect(localRead).not.toHaveBeenCalled();
  expect(api.executePagedLocalReview).toHaveBeenCalledTimes(1);
});

it("receives only server-authorized recovery entries instead of the physical daemon journal", async () => {
  const localRead = vi.fn().mockResolvedValue({ kind: "workspace_trash", path: "/repo", items: [{ path: "private.md", recovery_id: "another-context", directory: false }], next_offset: 1, has_more: false });
  vi.stubGlobal("daemonAPI", { readLocalReviewPage: localRead });
  api.getConfig.mockResolvedValue({ workspace_file_operations_supported: true });
  const filtered = { kind: "workspace_trash", path: "/repo", items: [], next_offset: 1, has_more: false };
  api.executePagedLocalReview.mockResolvedValue(filtered);

  expect(await requestReviewPage({ task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/repo", action: "workspace_trash" })).toEqual(filtered);

  expect(localRead).not.toHaveBeenCalled();
});

it("authorizes project directory reads through the server even on desktop", async () => {
  const localRead = vi.fn();
  vi.stubGlobal("daemonAPI", { readLocalReviewPage: localRead });
  api.supportsPagedLocalMR.mockResolvedValue(true);
  const files = { path: "/project", entries: [], truncated: false };
  api.executePagedLocalReview.mockResolvedValue(files);

  expect(await requestReviewPage({ workspace_id: "ws", project_resource_id: "resource", runtime_id: "runtime", path: "/project", action: "workspace_files" })).toEqual(files);

  expect(localRead).not.toHaveBeenCalled();
  expect(api.executePagedLocalReview).toHaveBeenCalledWith(expect.objectContaining({ task_id: "", project_resource_id: "resource" }), undefined);
});

it.each(["workspace_files", "file_content"] as const)("receives authoritative resource identity for %s instead of an unregistered local page", async (action) => {
  const fileId = "26d9d04d-5c45-40b6-bb58-c60e7d59433c";
  const directoryId = "b".repeat(64);
  vi.stubGlobal("daemonAPI", { readLocalReviewPage: async () => action === "workspace_files" ? { path: "/repo", entries: [], truncated: false } : { path: "notes.md", content: { text: "local", encoding: "utf8", offset: 0, next_offset: 5, size: 5, has_more: false } } });
  api.supportsPagedLocalMR.mockResolvedValue(true);
  const registered = action === "workspace_files" ? { path: "/repo", directory_id: directoryId, entries: [{ path: "notes.md", file_id: fileId, size: 4, directory: false }], truncated: false } : { path: "notes.md", file_id: fileId, directory_id: directoryId, content: { text: "body", encoding: "utf8", offset: 0, next_offset: 4, size: 4, has_more: false } };
  api.executePagedLocalReview.mockResolvedValue(registered);
  expect(await requestReviewPage({ task_id: "task", workspace_id: "ws", path: "/repo", action, relative_path: action === "file_content" ? "notes.md" : "." })).toEqual(registered);
});

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

it.each(["submit", "approve", "request_changes", "merge", "merge_selected", "stage", "unstage", "commit"] as const)("rejects retired %s before IPC, capability checks or relay", async (action) => {
  const local = vi.fn();
  vi.stubGlobal("daemonAPI", { readLocalReviewPage: local });
  await expect(requestReviewPage({ ...request, action })).rejects.toThrow("local_review_workflow_retired");
  expect(local).not.toHaveBeenCalled();
  expect(api.supportsPagedLocalMR).not.toHaveBeenCalled();
  expect(api.executePagedLocalReview).not.toHaveBeenCalled();
});
