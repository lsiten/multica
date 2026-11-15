// @vitest-environment node

import { describe, expect, it } from "vitest";
import { isPrintableRemoteText, remoteKeyCombo } from "./mirror-keyboard";

describe("mirror keyboard capture", () => {
  it("maps browser navigation and modifier chords to agent-desktop key syntax", () => {
    expect(remoteKeyCombo({
      key: "ArrowDown",
      metaKey: false,
      ctrlKey: true,
      altKey: false,
      shiftKey: true,
      isComposing: false,
    })).toBe("ctrl+shift+down");
    expect(remoteKeyCombo({
      key: "Enter",
      metaKey: false,
      ctrlKey: false,
      altKey: false,
      shiftKey: false,
      isComposing: false,
    })).toBe("return");
  });

  it("routes plain printable keys through the text fallback", () => {
    expect(isPrintableRemoteText({
      key: "a",
      metaKey: false,
      ctrlKey: false,
      altKey: false,
      isComposing: false,
    })).toBe(true);
    expect(remoteKeyCombo({
      key: "a",
      metaKey: false,
      ctrlKey: false,
      altKey: false,
      shiftKey: false,
      isComposing: false,
    })).toBe("a");
  });
});
