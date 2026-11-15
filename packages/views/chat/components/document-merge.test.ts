// @vitest-environment node

import { describe, expect, it } from "vitest";
import { mergeDocumentVersions, renderMergedDocument } from "./document-merge";

describe("document three-way merge", () => {
  it("combines non-overlapping local and server edits", () => {
    const result = mergeDocumentVersions({
      base: "alpha\nbeta\ngamma\n",
      local: "alpha local\nbeta\ngamma\n",
      remote: "alpha\nbeta\ngamma remote\n",
    });

    expect(result.conflictCount).toBe(0);
    expect(renderMergedDocument(result, {})).toBe(
      "alpha local\nbeta\ngamma remote\n",
    );
  });

  it("keeps one hunk when both sides make the same edit", () => {
    const result = mergeDocumentVersions({
      base: "alpha\nbeta\n",
      local: "alpha\nbeta shared\n",
      remote: "alpha\nbeta shared\n",
    });

    expect(result.conflictCount).toBe(0);
    expect(renderMergedDocument(result, {})).toBe("alpha\nbeta shared\n");
  });

  it("requires an explicit side choice for overlapping edits", () => {
    const result = mergeDocumentVersions({
      base: "alpha\nbeta\n",
      local: "alpha\nlocal\n",
      remote: "alpha\nremote\n",
    });
    const conflict = result.segments.find((segment) => segment.kind === "conflict");

    expect(result.conflictCount).toBe(1);
    expect(conflict?.kind).toBe("conflict");
    expect(renderMergedDocument(result, {})).toBeNull();
    if (!conflict) throw new Error("expected a conflict segment");
    expect(renderMergedDocument(result, { [conflict.id]: "local" })).toBe(
      "alpha\nlocal\n",
    );
    expect(renderMergedDocument(result, { [conflict.id]: "remote" })).toBe(
      "alpha\nremote\n",
    );
  });
});
