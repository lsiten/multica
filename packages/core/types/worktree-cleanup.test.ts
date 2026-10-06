// @vitest-environment node
import { expect, it } from "vitest";
import { parseWorktreeCleanupPreviews } from "./worktree-cleanup";

it("validates deletion preview identity and byte counts", () => {
  const row = { environment_id: "id", workspace_id: "ws", task_id: "task", revision: "revision", reason: "", original_bytes: 12, removed_bytes: 0, reclaimed: false };
  expect(parseWorktreeCleanupPreviews([row])[0]).toMatchObject({ environmentId: "id", originalBytes: 12, reclaimed: false });
  for (const malformed of [null, {}, [null], [{ ...row, original_bytes: -1 }], [{ ...row, reclaimed: "yes" }]]) {
    expect(() => parseWorktreeCleanupPreviews(malformed)).toThrow("Invalid worktree cleanup preview");
  }
});
