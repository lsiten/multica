// @vitest-environment node
import { expect, it } from "vitest";
import { pagedReviewRequestSchema, parsePagedReviewResponse } from "./local-review-pages";

const version = "a".repeat(64);
const index = "b".repeat(64);
const head = "c".repeat(40);
const scope = { task_id: "task", workspace_id: "ws", path: "/repo", target: "feature" };

it("parses index status without conflating it with a target diff manifest", () => {
  const request = pagedReviewRequestSchema.parse({ ...scope, action: "index", target: "" });
  expect(parsePagedReviewResponse(request, { kind: "index", version_id: version, status: { branch: "feature", head, index_id: index, files: [] } })).toMatchObject({ kind: "index", version_id: version });
});

it("requires reviewed index and explicit selected files for staging", () => {
  const request = { ...scope, action: "stage", version_id: version, snapshot_id: version, index_id: index, branch: "feature", head, paths: ["app.ts"], command_id: "stage-once" };
  expect(pagedReviewRequestSchema.parse(request)).toMatchObject(request);
  expect(() => pagedReviewRequestSchema.parse({ ...request, index_id: undefined })).toThrow();
  expect(() => pagedReviewRequestSchema.parse({ ...request, paths: [] })).toThrow();
});

it("rejects a commit result for another branch or index", () => {
  const request = pagedReviewRequestSchema.parse({ ...scope, action: "commit", version_id: version, snapshot_id: version, index_id: index, branch: "feature", head, message: "commit", command_id: "commit-once" });
  const result = { kind: "index_result", result: { commit: { commit: "d".repeat(40), parent: head, tree: "e".repeat(40), branch: "other", index_id: index } } };
  expect(() => parsePagedReviewResponse(request, result)).toThrow();
});
