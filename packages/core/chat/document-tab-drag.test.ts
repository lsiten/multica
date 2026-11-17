// @vitest-environment node

import { expect, it } from "vitest";
import { documentTabDragSessionId, documentTabDragValue } from "./document-tab-drag";

it("keeps the source session recoverable when a document is dragged to the desktop tab strip", () => {
  const value = documentTabDragValue("session-1", { kind: "workspace_file", id: "file-1", fileId: "file-1" });
  expect(documentTabDragSessionId(value)).toBe("session-1");
});

it("returns an empty session for malformed document drag data", () => {
  expect(documentTabDragSessionId("\nworkspace_file:file-1")).toBe("");
  expect(documentTabDragSessionId("not-a-session\nworkspace_file:file-1")).toBe("not-a-session");
});
