// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  PROCESS_CATEGORIES,
  processCategoryFromParam,
} from "./processes-overview-state";

describe("process overview URL state", () => {
  it.each(PROCESS_CATEGORIES)("restores the %s category", (category) => {
    expect(processCategoryFromParam(category)).toBe(category);
  });

  it("falls back to all for a missing or future category", () => {
    expect(processCategoryFromParam("")).toBe("all");
    expect(processCategoryFromParam("future-process")).toBe("all");
  });
});
