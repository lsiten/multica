import type { ReviewFilePage } from "@multica/core/types/local-review-pages";

export type ReviewCommentSide = "old" | "new";
export type ReviewFindingKind =
  | "debugger"
  | "console"
  | "type_bypass"
  | "todo"
  | "conflict_marker"
  | "empty_catch";
export type ReviewFindingSeverity = "high" | "medium" | "low";

export interface LocalReviewReply {
  readonly id: string;
  readonly body: string;
  readonly author: "member" | "agent";
  readonly createdAt: string;
}

export type ReviewFindingSuggestion =
  | {
      readonly kind: "remove_line";
      readonly label: string;
    }
  | {
      readonly kind: "replace_line";
      readonly label: string;
      readonly replacement: string;
    };

export interface LocalReviewComment {
  readonly id: string;
  readonly file: string;
  readonly side: ReviewCommentSide;
  readonly line: number;
  readonly revision: string;
  readonly body: string;
  readonly resolved: boolean;
  readonly author: "member" | "agent";
  readonly kind?: ReviewFindingKind;
  readonly severity?: ReviewFindingSeverity;
  readonly replies: readonly LocalReviewReply[];
  readonly suggestion?: ReviewFindingSuggestion;
}

const findingKinds = new Set<ReviewFindingKind>([
  "debugger",
  "console",
  "type_bypass",
  "todo",
  "conflict_marker",
  "empty_catch",
]);

export function parseLocalReviewComments(value: string | null): readonly LocalReviewComment[] {
  if (!value) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(value);
  } catch (error) {
    if (error instanceof SyntaxError) return [];
    throw error;
  }
  if (!Array.isArray(parsed)) return [];
  return parsed.flatMap((entry): readonly LocalReviewComment[] => {
    if (!entry || typeof entry !== "object") return [];
    const candidate: Partial<LocalReviewComment> = entry;
    if (
      typeof candidate.id !== "string" ||
      typeof candidate.file !== "string" ||
      (candidate.side !== "old" && candidate.side !== "new") ||
      typeof candidate.line !== "number" ||
      !Number.isInteger(candidate.line) ||
      candidate.line <= 0 ||
      typeof candidate.revision !== "string" ||
      typeof candidate.body !== "string" ||
      typeof candidate.resolved !== "boolean" ||
      (candidate.author !== "member" && candidate.author !== "agent") ||
      (candidate.kind !== undefined && !findingKinds.has(candidate.kind))
    ) {
      return [];
    }
    const suggestion = parseSuggestion(candidate.suggestion);
    return [{
      ...candidate,
      id: candidate.id,
      file: candidate.file,
      side: candidate.side,
      line: candidate.line,
      revision: candidate.revision,
      body: candidate.body,
      resolved: candidate.resolved,
      author: candidate.author,
      replies: parseReplies(candidate.replies),
      ...(suggestion ? { suggestion } : {}),
    }];
  });
}

export function reviewCommentId(input: {
  readonly file: string;
  readonly revision: string;
  readonly side: ReviewCommentSide;
  readonly line: number;
  readonly body?: string;
}): string {
  return [input.file, input.revision, input.side, input.line, input.body ?? ""].join(":");
}

export function reviewFindingsForPatch(input: {
  readonly file: string;
  readonly revision: string;
  readonly page: NonNullable<ReviewFilePage["page"]>;
}): readonly LocalReviewComment[] {
  const findings: LocalReviewComment[] = [];
  const lines = input.page.lines;
  lines.forEach((line, index) => {
    const newLine = line.new_line;
    if (line.kind !== "add" || newLine === undefined) return;
    const text = line.text;
    const add = (kind: ReviewFindingKind, severity: ReviewFindingSeverity) => {
      findings.push({
        id: reviewCommentId({
          file: input.file,
          revision: input.revision,
          side: "new",
          line: newLine,
          body: kind,
        }),
        file: input.file,
        side: "new",
        line: newLine,
        revision: input.revision,
        body: kind,
        resolved: false,
        author: "agent",
        kind,
        severity,
        replies: [],
        ...findingSuggestion(kind, text),
      });
    };
    if (/\bdebugger\b/.test(text)) add("debugger", "high");
    else if (/\bconsole\.(?:log|debug|warn)\s*\(/.test(text)) add("console", "medium");
    else if (/(?:\bas\s+any\b|@ts-ignore|@ts-expect-error)/.test(text)) add("type_bypass", "high");
    else if (/\b(?:TODO|FIXME)\b/.test(text)) add("todo", "low");
    else if (/^(?:\+)?(?:<<<<<<<|=======|>>>>>>>)\b/.test(text)) add("conflict_marker", "high");
    else {
      const next = lines[index + 1];
      if (/\bcatch\s*\([^)]*\)\s*\{\s*$/.test(text) && next?.text.trim() === "}") {
        add("empty_catch", "medium");
      }
    }
  });
  return findings;
}

export function appendLocalReviewReply(
  comments: readonly LocalReviewComment[],
  commentId: string,
  reply: LocalReviewReply,
): readonly LocalReviewComment[] {
  return comments.map((comment) => comment.id === commentId
    ? { ...comment, replies: [...comment.replies, reply] }
    : comment);
}

export function applyReviewFindingSuggestion(
  content: string,
  finding: LocalReviewComment,
): string | null {
  if (!finding.suggestion) return null;
  const lines = content.split("\n");
  const index = finding.line - 1;
  if (index < 0 || index >= lines.length) return null;
  if (finding.suggestion.kind === "remove_line") {
    lines.splice(index, 1);
  } else {
    lines[index] = finding.suggestion.replacement;
  }
  return lines.join("\n");
}

function findingSuggestion(
  kind: ReviewFindingKind,
  text: string,
): Pick<LocalReviewComment, "suggestion"> | Record<string, never> {
  if (kind === "debugger" || kind === "console" || kind === "conflict_marker") {
    return {
      suggestion: {
        kind: "remove_line",
        label: "Remove line",
      },
    };
  }
  if (kind === "type_bypass") {
    const replacement = text
      .replace(/@ts-(?:ignore|expect-error)\s*/g, "")
      .replace(/\bas\s+any\b/g, "as unknown");
    return replacement !== text
      ? {
          suggestion: {
            kind: "replace_line",
            label: "Remove type escape",
            replacement,
          },
        }
      : {};
  }
  return {};
}

function parseReplies(value: unknown): readonly LocalReviewReply[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((entry): readonly LocalReviewReply[] => {
    if (!entry || typeof entry !== "object") return [];
    const candidate: Partial<LocalReviewReply> = entry;
    return typeof candidate.id === "string" &&
      typeof candidate.body === "string" &&
      (candidate.author === "member" || candidate.author === "agent") &&
      typeof candidate.createdAt === "string"
      ? [{
          id: candidate.id,
          body: candidate.body,
          author: candidate.author,
          createdAt: candidate.createdAt,
        }]
      : [];
  });
}

function parseSuggestion(value: unknown): ReviewFindingSuggestion | null {
  if (!value || typeof value !== "object") return null;
  const candidate: Partial<ReviewFindingSuggestion> = value;
  if (candidate.kind === "remove_line" && typeof candidate.label === "string") {
    return { kind: "remove_line", label: candidate.label };
  }
  if (
    candidate.kind === "replace_line" &&
    typeof candidate.label === "string" &&
    typeof candidate.replacement === "string"
  ) {
    return {
      kind: "replace_line",
      label: candidate.label,
      replacement: candidate.replacement,
    };
  }
  return null;
}
