// @vitest-environment node
import { expect, it } from "vitest";
import { parseWorktreeArchiveResults, parseWorktreeArchiveSummaries, worktreeArchiveRequestSchema } from "./worktree-archives";

it("requires stable environment identities, preview revisions and an operation identity", () => {
  expect(() => worktreeArchiveRequestSchema.parse({ action: "archive", selections: [{ environmentId: "/root", revision: "" }] })).toThrow();
  expect(() => worktreeArchiveRequestSchema.parse({ action: "restore", archiveId: "/" })).toThrow();
  expect(worktreeArchiveRequestSchema.parse({ action: "archive", operationId: "a".repeat(64), selections: [{ environmentId: "b".repeat(64), revision: "c".repeat(64) }] }).action).toBe("archive");
});

it("keeps malformed archive responses from authorizing a successful reclamation", () => {
  const result = { environment_id: "env", workspace_id: "ws", task_id: "task", revision: "revision", archive_id: "id", reason: "", original_bytes: 100, archive_bytes: 20, reclaimed: true, restored: false };
  expect(parseWorktreeArchiveResults([result])[0]).toMatchObject({ environmentId: "env", originalBytes: 100, reclaimed: true, restored: false });
  expect(() => parseWorktreeArchiveResults([{ ...result, reclaimed: "yes" }])).toThrow();
  expect(() => parseWorktreeArchiveResults([{ ...result, archive_bytes: -1 }])).toThrow();
  expect(() => parseWorktreeArchiveSummaries([{ archive_id: "/root" }])).toThrow();
});
