// @vitest-environment node
import { expect, it, vi } from "vitest";
import { requestWorktreeArchive } from "./worktree-archive-request";

it("forwards only validated archive identities and converts the wire contract", async () => {
  const send = vi.fn().mockResolvedValue([]);
  await expect(requestWorktreeArchive({ action: "archive", operationId: "a".repeat(64), selections: [{ environmentId: "/root", revision: "" }] }, send)).rejects.toThrow();
  expect(send).not.toHaveBeenCalled();
  await requestWorktreeArchive({ action: "archive", workspaceId: "ws", operationId: "a".repeat(64), selections: [{ environmentId: "b".repeat(64), revision: "c".repeat(64) }] }, send);
  expect(send).toHaveBeenCalledWith({ action: "archive", workspace_id: "ws", operation_id: "a".repeat(64), selections: [{ environment_id: "b".repeat(64), revision: "c".repeat(64) }] });
});

it("uses archive ids rather than caller-selected filesystem paths for restore", async () => {
  const send = vi.fn().mockResolvedValue([]);
  await requestWorktreeArchive({ action: "restore", workspaceId: "ws", archiveId: "a".repeat(64) }, send);
  expect(send).toHaveBeenCalledWith({ action: "restore", workspace_id: "ws", archive_id: "a".repeat(64) });
});
