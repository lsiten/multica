// @vitest-environment node
import { expect, it } from "vitest";
import { defaultReviewTarget } from "./local-review-target";

it.each([
  [["test", "production", "master", "main"], "", ""],
  [["feature", "release"], "release", "release"],
  [["feature", "release"], "main", ""],
  [[], "main", ""],
])("uses only an available requested target from %j", (branches, requested, expected) => {
  expect(defaultReviewTarget(branches, requested)).toBe(expected);
});
