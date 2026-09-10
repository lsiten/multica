// @vitest-environment node
import { expect, it } from "vitest";
import { pagedReviewRequestSchema, parsePagedReviewResponse } from "./local-review-pages";

const version = "a".repeat(64);
const request = { task_id: "task", workspace_id: "ws", path: "/repo", target: "main", action: "merge_selected", version_id: version, snapshot_id: version, paths: ["chosen.ts"], message: "apply chosen", command_id: "once" } as const;
const result = { kind: "selected_merge", version_id: version, target: "main", paths: ["chosen.ts"], commit: "b".repeat(40), conflicts: [] };
it("binds a selected merge response to version, target and exact file selection", () => {
  const parsed = pagedReviewRequestSchema.parse(request);
  expect(parsePagedReviewResponse(parsed, result)).toEqual(result);
  expect(() => parsePagedReviewResponse(parsed, { ...result, paths: ["unselected.ts"] })).toThrow();
  expect(() => parsePagedReviewResponse(parsed, { ...result, target: "other" })).toThrow();
  expect(() => parsePagedReviewResponse(parsed, { ...result, version_id: "c".repeat(64) })).toThrow();
});
it("keeps conflict responses distinct from successful publication", () => {
  const parsed = pagedReviewRequestSchema.parse(request);
  expect(parsePagedReviewResponse(parsed, { ...result, commit: "", conflicts: ["chosen.ts"] })).toMatchObject({ commit: "", conflicts: ["chosen.ts"] });
  expect(() => parsePagedReviewResponse(parsed, { ...result, conflicts: ["chosen.ts"] })).toThrow();
  expect(() => pagedReviewRequestSchema.parse({ ...request, paths: ["chosen.ts", "chosen.ts"] })).toThrow();
});
