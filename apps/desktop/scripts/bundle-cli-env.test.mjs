// @vitest-environment node

import { describe, expect, it } from "vitest";
import { cgoEnabledForGoos } from "./bundle-cli-env.mjs";

describe("cgoEnabledForGoos", () => {
  it("enables CGO for macOS screen capture", () => {
    expect(cgoEnabledForGoos("darwin")).toBe("1");
  });

  it("keeps non-mac daemon builds CGO-free", () => {
    expect(cgoEnabledForGoos("windows")).toBe("0");
    expect(cgoEnabledForGoos("linux")).toBe("0");
  });
});
