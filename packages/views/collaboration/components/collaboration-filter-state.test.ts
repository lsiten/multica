import { describe, expect, it } from "vitest";
import {
  collaborationActivityFromParam,
  collaborationDateBoundary,
  collaborationStatusFromParam,
  collaborationViewFromParam,
} from "./collaboration-filter-state";

describe("collaboration URL filters", () => {
  it.each([
    ["active", "active"],
    ["all", "all"],
    ["ended", "ended"],
  ] as const)("restores the %s activity filter", (value, expected) => {
    expect(collaborationActivityFromParam(value)).toBe(expected);
  });

  it("safely falls back for unknown activity and status values", () => {
    expect(collaborationActivityFromParam("future")).toBe("active");
    expect(collaborationStatusFromParam("future")).toBe("");
    expect(collaborationStatusFromParam("failed")).toBe("failed");
  });

  it("restores the graph or accessible list view", () => {
    expect(collaborationViewFromParam("list")).toBe("list");
    expect(collaborationViewFromParam("graph")).toBe("graph");
    expect(collaborationViewFromParam("future")).toBe("graph");
  });

  it("turns date inputs into local-day boundaries and rejects malformed values", () => {
    expect(collaborationDateBoundary("2026-11-15", false)).toBe(
      new Date("2026-11-15T00:00:00.000").toISOString(),
    );
    expect(collaborationDateBoundary("2026-11-15", true)).toBe(
      new Date("2026-11-15T23:59:59.999").toISOString(),
    );
    expect(collaborationDateBoundary("", false)).toBeUndefined();
    expect(collaborationDateBoundary("not-a-date", true)).toBeUndefined();
    expect(collaborationDateBoundary("2026-02-31", true)).toBeUndefined();
  });
});
