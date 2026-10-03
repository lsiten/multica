// @vitest-environment node
import { expect, it, vi } from "vitest";
import { requestWorktreeCaches } from "./worktree-cache-request";

it("sends a scoped preview without authorizing deletion", async () => {
  const send = vi.fn().mockResolvedValue([]);
  await requestWorktreeCaches({ action: "preview_cache", workspace_id: "workspace" }, send);
  expect(send).toHaveBeenCalledWith({ action: "preview_cache", workspace_id: "workspace" });
});

it("requires environment ids and revisions before sending a deletion", async () => {
  const send = vi.fn().mockResolvedValue([]);
  await expect(requestWorktreeCaches({ action: "clean_cache", selections: [{ environmentId: "/tmp/root", revision: "" }] }, send)).rejects.toThrow();
  await expect(requestWorktreeCaches({ action: "clean_cache", selections: [] }, send)).rejects.toThrow();
  expect(send).not.toHaveBeenCalled();
  await requestWorktreeCaches({ action: "clean_cache", workspace_id: "workspace", selections: [{ environmentId: "a".repeat(64), revision: "b".repeat(64) }] }, send);
  expect(send).toHaveBeenCalledWith({ action: "clean_cache", workspace_id: "workspace", selections: [{ environment_id: "a".repeat(64), revision: "b".repeat(64) }] });
});
