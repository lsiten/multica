import { describe, expect, it } from "vitest";
import { appSwitchModifiers } from "./mirror-shortcuts";

describe("application switch shortcut", () => {
  it("uses Command+Tab on macOS", () => {
    expect(appSwitchModifiers("macos")).toEqual(["meta"]);
    expect(appSwitchModifiers("darwin-arm64")).toEqual(["meta"]);
  });

  it("uses Alt+Tab on other desktop platforms", () => {
    expect(appSwitchModifiers("windows")).toEqual(["alt"]);
    expect(appSwitchModifiers(undefined)).toEqual(["alt"]);
  });
});
