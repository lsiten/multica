export interface ReviewDiffLine {
  readonly text: string;
  readonly kind: "context" | "add" | "remove" | "header";
  readonly oldLine?: number;
  readonly newLine?: number;
}

/** Preserves Git patch text and derives source line numbers from each hunk header. */
export function reviewDiffLines(patch: string, untracked = false): ReviewDiffLine[] {
  let oldLine = 0;
  let newLine = 0;
  let inHunk = false;
  const lines = patch.split("\n");
  if (lines.at(-1) === "") lines.pop();
  if (untracked && !patch.startsWith("Binary file SHA256:")) {
    return lines.map((text, index) => ({ text, kind: "add", newLine: index + 1 }));
  }
  return lines.map((text) => {
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(text);
    if (hunk) {
      oldLine = Number(hunk[1]);
      newLine = Number(hunk[2]);
      inHunk = true;
      return { text, kind: "header" };
    }
    if (!inHunk || text.startsWith("\\")) return { text, kind: "header" };
    if (text.startsWith("+")) return { text, kind: "add", newLine: newLine++ };
    if (text.startsWith("-")) return { text, kind: "remove", oldLine: oldLine++ };
    if (text.startsWith(" ")) return { text, kind: "context", oldLine: oldLine++, newLine: newLine++ };
    inHunk = false;
    return { text, kind: "header" };
  });
}
