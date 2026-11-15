import { diffTraceLines } from "../../common/task-transcript/trace-event-presenter";

export interface DocumentMergeConflict {
  readonly id: string;
  readonly kind: "conflict";
  readonly base: readonly string[];
  readonly local: readonly string[];
  readonly remote: readonly string[];
}

export interface DocumentMergeLines {
  readonly id: string;
  readonly kind: "lines";
  readonly lines: readonly string[];
}

export type DocumentMergeSegment = DocumentMergeLines | DocumentMergeConflict;

export interface DocumentMergeResult {
  readonly segments: readonly DocumentMergeSegment[];
  readonly conflictCount: number;
}

interface ChangeHunk {
  readonly start: number;
  readonly end: number;
  readonly lines: readonly string[];
}

export function mergeDocumentVersions(input: {
  readonly base: string;
  readonly local: string;
  readonly remote: string;
}): DocumentMergeResult {
  const base = input.base.split("\n");
  const local = changeHunks(base, input.local.split("\n"));
  const remote = changeHunks(base, input.remote.split("\n"));
  const segments: DocumentMergeSegment[] = [];
  let cursor = 0;
  let localIndex = 0;
  let remoteIndex = 0;

  while (cursor < base.length || localIndex < local.length || remoteIndex < remote.length) {
    const localHunk = nextHunk(local, localIndex, cursor);
    const remoteHunk = nextHunk(remote, remoteIndex, cursor);
    if (!localHunk && !remoteHunk) {
      if (cursor < base.length) {
        pushLines(segments, base.slice(cursor), cursor);
      }
      break;
    }
    const next = earliest(localHunk, remoteHunk);
    if (!next) break;
    if (next.start > cursor) {
      pushLines(segments, base.slice(cursor, next.start), cursor);
      cursor = next.start;
    }

    if (
      localHunk &&
      remoteHunk &&
      localHunk.start === remoteHunk.start &&
      localHunk.end === remoteHunk.end &&
      sameLines(localHunk.lines, remoteHunk.lines)
    ) {
      pushLines(segments, localHunk.lines, localHunk.start);
      cursor = localHunk.end;
      localIndex++;
      remoteIndex++;
      continue;
    }

    if (localHunk && remoteHunk && hunksOverlap(localHunk, remoteHunk)) {
      const group = groupedConflict(local, localIndex, remote, remoteIndex);
      const localLines = applyHunksToRange(
        base,
        group.start,
        group.end,
        group.local,
      );
      const remoteLines = applyHunksToRange(
        base,
        group.start,
        group.end,
        group.remote,
      );
      if (sameLines(localLines, remoteLines)) {
        pushLines(segments, localLines, group.start);
      } else {
        segments.push({
          id: `conflict-${group.start}-${group.end}`,
          kind: "conflict",
          base: base.slice(group.start, group.end),
          local: localLines,
          remote: remoteLines,
        });
      }
      cursor = group.end;
      localIndex = group.localEnd;
      remoteIndex = group.remoteEnd;
      continue;
    }

    if (localHunk && (!remoteHunk || comesBefore(localHunk, remoteHunk))) {
      pushLines(segments, localHunk.lines, localHunk.start);
      cursor = localHunk.end;
      localIndex++;
    } else if (remoteHunk) {
      pushLines(segments, remoteHunk.lines, remoteHunk.start);
      cursor = remoteHunk.end;
      remoteIndex++;
    }
  }

  return {
    segments: compactSegments(segments),
    conflictCount: segments.filter((segment) => segment.kind === "conflict").length,
  };
}

export function renderMergedDocument(
  result: DocumentMergeResult,
  choices: Readonly<Record<string, "local" | "remote">>,
): string | null {
  const lines: string[] = [];
  for (const segment of result.segments) {
    if (segment.kind === "lines") {
      lines.push(...segment.lines);
      continue;
    }
    const choice = choices[segment.id];
    if (!choice) return null;
    lines.push(...(choice === "local" ? segment.local : segment.remote));
  }
  return lines.join("\n");
}

function changeHunks(base: readonly string[], next: readonly string[]): readonly ChangeHunk[] {
  const hunks: ChangeHunk[] = [];
  let baseIndex = 0;
  let active: { start: number; end: number; lines: string[] } | null = null;
  const flush = () => {
    if (!active) return;
    hunks.push({
      start: active.start,
      end: active.end,
      lines: active.lines,
    });
    active = null;
  };
  for (const line of diffTraceLines([...base], [...next])) {
    if (line.kind === "context") {
      flush();
      baseIndex++;
      continue;
    }
    active ??= { start: baseIndex, end: baseIndex, lines: [] };
    if (line.kind === "remove") {
      active.end++;
      baseIndex++;
    } else if (line.kind === "add") {
      active.lines.push(line.text);
    }
  }
  flush();
  return hunks;
}

function nextHunk(
  hunks: readonly ChangeHunk[],
  index: number,
  cursor: number,
): ChangeHunk | undefined {
  let next = index;
  while (next < hunks.length) {
    const hunk = hunks[next];
    if (!hunk || hunk.end >= cursor) break;
    next++;
  }
  return hunks[next];
}

function earliest(
  first: ChangeHunk | undefined,
  second: ChangeHunk | undefined,
): ChangeHunk | undefined {
  if (!first) return second;
  if (!second) return first;
  return comesBefore(first, second) || first.start === second.start ? first : second;
}

function comesBefore(first: ChangeHunk, second: ChangeHunk): boolean {
  return first.start < second.start ||
    (first.start === second.start && first.end < second.end);
}

function sameLines(first: readonly string[], second: readonly string[]): boolean {
  return first.length === second.length &&
    first.every((line, index) => line === second[index]);
}

function hunksOverlap(first: ChangeHunk, second: ChangeHunk): boolean {
  return rangesOverlap(first.start, first.end, second.start, second.end);
}

function rangesOverlap(
  firstStart: number,
  firstEnd: number,
  secondStart: number,
  secondEnd: number,
): boolean {
  if (firstStart === firstEnd && secondStart === secondEnd) {
    return firstStart === secondStart;
  }
  if (firstStart === firstEnd) {
    return firstStart >= secondStart && firstStart < secondEnd;
  }
  if (secondStart === secondEnd) {
    return secondStart >= firstStart && secondStart < firstEnd;
  }
  return firstStart < secondEnd && secondStart < firstEnd;
}

function groupedConflict(
  local: readonly ChangeHunk[],
  localIndex: number,
  remote: readonly ChangeHunk[],
  remoteIndex: number,
): {
  readonly start: number;
  readonly end: number;
  readonly local: readonly ChangeHunk[];
  readonly remote: readonly ChangeHunk[];
  readonly localEnd: number;
  readonly remoteEnd: number;
} {
  const firstLocal = local[localIndex];
  const firstRemote = remote[remoteIndex];
  let start = Math.min(firstLocal?.start ?? 0, firstRemote?.start ?? 0);
  let end = Math.max(firstLocal?.end ?? 0, firstRemote?.end ?? 0);
  const selectedLocal: ChangeHunk[] = [];
  const selectedRemote: ChangeHunk[] = [];
  let localEnd = localIndex;
  let remoteEnd = remoteIndex;
  let extended = true;
  while (extended) {
    extended = false;
    while (localEnd < local.length) {
      const hunk = local[localEnd];
      if (!hunk || !rangesOverlap(start, end, hunk.start, hunk.end)) break;
      selectedLocal.push(hunk);
      start = Math.min(start, hunk.start);
      end = Math.max(end, hunk.end);
      localEnd++;
      extended = true;
    }
    while (remoteEnd < remote.length) {
      const hunk = remote[remoteEnd];
      if (!hunk || !rangesOverlap(start, end, hunk.start, hunk.end)) break;
      selectedRemote.push(hunk);
      start = Math.min(start, hunk.start);
      end = Math.max(end, hunk.end);
      remoteEnd++;
      extended = true;
    }
  }
  return {
    start,
    end,
    local: selectedLocal,
    remote: selectedRemote,
    localEnd,
    remoteEnd,
  };
}

function applyHunksToRange(
  base: readonly string[],
  start: number,
  end: number,
  hunks: readonly ChangeHunk[],
): readonly string[] {
  const lines: string[] = [];
  let cursor = start;
  for (const hunk of hunks) {
    if (hunk.start < start || hunk.end > end) continue;
    lines.push(...base.slice(cursor, hunk.start), ...hunk.lines);
    cursor = hunk.end;
  }
  lines.push(...base.slice(cursor, end));
  return lines;
}

function pushLines(
  segments: DocumentMergeSegment[],
  lines: readonly string[],
  position: number,
): void {
  if (!lines.length) return;
  const previous = segments.at(-1);
  if (previous?.kind === "lines") {
    segments[segments.length - 1] = {
      ...previous,
      lines: [...previous.lines, ...lines],
    };
    return;
  }
  segments.push({
    id: `lines-${position}-${lines.length}`,
    kind: "lines",
    lines,
  });
}

function compactSegments(
  segments: readonly DocumentMergeSegment[],
): readonly DocumentMergeSegment[] {
  const compact: DocumentMergeSegment[] = [];
  for (const segment of segments) {
    const previous = compact.at(-1);
    if (segment.kind === "lines" && previous?.kind === "lines") {
      compact[compact.length - 1] = {
        ...previous,
        lines: [...previous.lines, ...segment.lines],
      };
    } else {
      compact.push(segment);
    }
  }
  return compact;
}
