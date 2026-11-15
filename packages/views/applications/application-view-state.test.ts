import { describe, expect, it } from "vitest";
import {
  applicationTabFromParam,
  applicationViewFromParam,
} from "./application-view-state";

describe("application URL state", () => {
  it.each([
    ["projects", "projects"],
    ["runtimes", "runtimes"],
    ["board", "board"],
  ] as const)("restores the %s application view", (value, expected) => {
    expect(applicationViewFromParam(value)).toBe(expected);
  });

  it("falls back to projects for a missing or unknown view", () => {
    expect(applicationViewFromParam("")).toBe("projects");
    expect(applicationViewFromParam("future-view")).toBe("projects");
  });

  it.each([
    ["instances", "instances"],
    ["configuration", "configuration"],
    ["orchestration", "orchestration"],
    ["activity", "activity"],
    ["logs", "logs"],
  ] as const)("restores the %s application tab", (value, expected) => {
    expect(applicationTabFromParam(value)).toBe(expected);
  });

  it("falls back to instances for an unknown application tab", () => {
    expect(applicationTabFromParam("future-tab")).toBe("instances");
  });
});
