// @vitest-environment node
import { expect, it } from "vitest";
import { defaultReviewTarget } from "./local-review-target";

it.each([
  [["test", "production", "master", "main"], "main"],
  [["test", "production", "master"], "master"],
  [["test", "production"], "production"],
  [["feature", "test"], "test"],
  [["feature", "release"], "feature"],
  [[], ""],
])("selects a default from %j", (branches, expected) => {
  expect(defaultReviewTarget(branches)).toBe(expected);
});
