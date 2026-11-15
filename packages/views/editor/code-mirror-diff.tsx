"use client";

import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
} from "react";
import { basicSetup } from "codemirror";
import { EditorState, type Range } from "@codemirror/state";
import {
  EditorView,
  GutterMarker,
  Decoration,
  ViewPlugin,
  gutter,
} from "@codemirror/view";
import { LanguageDescription } from "@codemirror/language";
import { languages } from "@codemirror/language-data";
import {
  MergeView,
  goToNextChunk,
  goToPreviousChunk,
  unifiedMergeView,
} from "@codemirror/merge";

export interface CodeMirrorDiffHandle {
  nextChange(): boolean;
  previousChange(): boolean;
  selectedNewLine(): number;
  selectedOldLine(): number;
  goToLine(line: number, side?: "old" | "new"): void;
  focus(): void;
}

export interface DiffAnnotationMarker {
  readonly line: number;
  readonly resolved?: boolean;
}

class AnnotationMarker extends GutterMarker {
  constructor(readonly resolved: boolean) {
    super();
  }

  eq(other: AnnotationMarker): boolean {
    return this.resolved === other.resolved;
  }

  toDOM(): Node {
    const marker = document.createElement("span");
    marker.className = this.resolved
      ? "cm-review-annotation cm-review-annotation-resolved"
      : "cm-review-annotation";
    marker.setAttribute(
      "aria-label",
      this.resolved ? "Resolved review comment" : "Open review comment",
    );
    marker.textContent = this.resolved ? "✓" : "●";
    return marker;
  }
}

const annotationGutter = (annotations: readonly DiffAnnotationMarker[]) => {
  const byLine = new Map(
    annotations.map((annotation) => [annotation.line, annotation]),
  );
  return gutter({
    class: "cm-review-gutter",
    lineMarker(view, line) {
      const number = view.state.doc.lineAt(line.from).number;
      const annotation = byLine.get(number);
      return annotation
        ? new AnnotationMarker(annotation.resolved === true)
        : null;
    },
    initialSpacer: () => new AnnotationMarker(false),
  });
};

const diffTheme = EditorView.theme({
  "&": {
    height: "100%",
    backgroundColor: "transparent",
    color: "var(--foreground)",
  },
  ".cm-scroller": {
    overflow: "auto",
    fontFamily:
      "var(--font-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace)",
  },
  ".cm-content": {
    padding: "0.5rem 0",
  },
  ".cm-gutters": {
    backgroundColor: "var(--surface)",
    color: "var(--muted-foreground)",
    borderRight: "1px solid var(--surface-border)",
  },
  ".cm-changedLine, .cm-deletedLine": {
    backgroundColor:
      "color-mix(in oklab, var(--success) 12%, transparent)",
  },
  ".cm-insertedLine": {
    backgroundColor:
      "color-mix(in oklab, var(--success) 14%, transparent)",
  },
  ".cm-changedText": {
    backgroundColor:
      "color-mix(in oklab, var(--success) 22%, transparent)",
  },
  ".cm-deletedText": {
    backgroundColor:
      "color-mix(in oklab, var(--destructive) 20%, transparent)",
  },
  ".cm-changedLineGutter": {
    backgroundColor:
      "color-mix(in oklab, var(--success) 16%, transparent)",
  },
  ".cm-deletedLineGutter": {
    backgroundColor:
      "color-mix(in oklab, var(--destructive) 14%, transparent)",
  },
  ".cm-mergeView": {
    height: "100%",
  },
  ".cm-mergeViewEditors": {
    height: "100%",
  },
  ".cm-review-gutter": {
    width: "1.25rem",
  },
  ".cm-review-annotation": {
    color: "var(--warning)",
    fontSize: "0.85rem",
  },
  ".cm-review-annotation-resolved": {
    color: "var(--success)",
  },
  ".cm-whitespace-visible": {
    boxShadow:
      "inset 0 -1px color-mix(in oklab, var(--muted-foreground) 55%, transparent)",
  },
  "&.cm-focused": {
    outline: "none",
  },
});

export const CodeMirrorDiff = forwardRef<
  CodeMirrorDiffHandle,
  {
    readonly original: string;
    readonly modified: string;
    readonly path: string;
    readonly layout: "split" | "unified";
    readonly annotations?: readonly DiffAnnotationMarker[];
    readonly newLineNumbers?: readonly (number | undefined)[];
    readonly oldLineNumbers?: readonly (number | undefined)[];
    readonly wrap?: boolean;
    readonly showWhitespace?: boolean;
  }
>(function CodeMirrorDiff(
  {
    original,
    modified,
    path,
    layout,
    annotations = [],
    newLineNumbers,
    oldLineNumbers,
    wrap = true,
    showWhitespace = false,
  },
  ref,
) {
  const host = useRef<HTMLDivElement>(null);
  const activeView = useRef<EditorView | null>(null);
  const activeOldView = useRef<EditorView | null>(null);
  const destroy = useRef<(() => void) | null>(null);

  useImperativeHandle(ref, () => ({
    nextChange() {
      const editor = activeView.current;
      return editor
        ? goToNextChunk({ state: editor.state, dispatch: editor.dispatch })
        : false;
    },
    previousChange() {
      const editor = activeView.current;
      return editor
        ? goToPreviousChunk({ state: editor.state, dispatch: editor.dispatch })
        : false;
    },
    selectedNewLine() {
      const editor = activeView.current;
      if (!editor) return 1;
      const position = editor.state.doc.lineAt(
        editor.state.selection.main.head,
      ).number;
      return newLineNumbers?.[position - 1] ?? position;
    },
    selectedOldLine() {
      const editor = activeOldView.current ?? activeView.current;
      if (!editor) return 1;
      const position = editor.state.doc.lineAt(
        editor.state.selection.main.head,
      ).number;
      return oldLineNumbers?.[position - 1] ?? position;
    },
    goToLine(line, side = "new") {
      const editor = side === "old" && activeOldView.current
        ? activeOldView.current
        : activeView.current;
      if (!editor) return;
      const lineNumbers = side === "old" ? oldLineNumbers : newLineNumbers;
      const mapped = lineNumbers?.findIndex((number) => number === line) ?? -1;
      const documentLine = mapped >= 0 ? mapped + 1 : line;
      const target = editor.state.doc.line(
        Math.min(Math.max(1, documentLine), editor.state.doc.lines),
      );
      editor.dispatch({
        selection: { anchor: target.from },
        effects: EditorView.scrollIntoView(target.from, { y: "center" }),
      });
      editor.focus();
    },
    focus() {
      activeView.current?.focus();
    },
  }), [newLineNumbers, oldLineNumbers]);

  useEffect(() => {
    const parent = host.current;
    if (!parent) return;
    let disposed = false;
    let localDestroy: (() => void) | null = null;
    const language = LanguageDescription.matchFilename(languages, path);
    const readOnly = [EditorState.readOnly.of(true), EditorView.editable.of(false)];
    const shared = (support: readonly import("@codemirror/state").Extension[]) => [
      basicSetup,
      diffTheme,
      ...(wrap ? [EditorView.lineWrapping] : []),
      ...(showWhitespace ? [whitespaceDecorations] : []),
      annotationGutter(annotations),
      ...support,
    ];
    void (async () => {
      let support: readonly import("@codemirror/state").Extension[] = [];
      if (language) {
        try {
          support = [(await language.load()).support];
        } catch {
          // Unknown or unavailable languages still get a plain, usable diff.
        }
      }
      if (disposed) return;
      if (layout === "split") {
        const merge = new MergeView({
          parent,
          a: {
            doc: original,
            extensions: [...shared(support), readOnly],
          },
          b: {
            doc: modified,
            extensions: [...shared(support), readOnly],
          },
          orientation: "a-b",
          gutter: true,
          collapseUnchanged: { margin: 3, minSize: 4 },
          highlightChanges: true,
        });
        activeView.current = merge.b;
        activeOldView.current = merge.a;
        localDestroy = () => merge.destroy();
      } else {
        const editor = new EditorView({
          parent,
          state: EditorState.create({
            doc: modified,
            extensions: [
              ...shared(support),
              readOnly,
              unifiedMergeView({
                original,
                gutter: true,
                collapseUnchanged: { margin: 3, minSize: 4 },
                mergeControls: false,
                syntaxHighlightDeletions: true,
              }),
            ],
          }),
        });
        activeView.current = editor;
        activeOldView.current = null;
        localDestroy = () => editor.destroy();
      }
      destroy.current = localDestroy;
    })();

    return () => {
      disposed = true;
      activeView.current = null;
      activeOldView.current = null;
      localDestroy?.();
      if (destroy.current === localDestroy) destroy.current = null;
      destroy.current?.();
      parent.replaceChildren();
    };
  }, [annotations, layout, modified, original, path, showWhitespace, wrap]);

  return (
    <div
      ref={host}
      className="h-full min-h-0 overflow-hidden [&_.cm-mergeView]:h-full [&_.cm-mergeViewEditors]:h-full"
    />
  );
});

const whitespaceMark = Decoration.mark({ class: "cm-whitespace-visible" });

const whitespaceDecorations = ViewPlugin.fromClass(class {
  decorations;

  constructor(view: EditorView) {
    this.decorations = decorateWhitespace(view);
  }

  update(update: import("@codemirror/view").ViewUpdate) {
    if (update.docChanged || update.viewportChanged) {
      this.decorations = decorateWhitespace(update.view);
    }
  }
}, {
  decorations: (plugin) => plugin.decorations,
});

function decorateWhitespace(view: EditorView) {
  const ranges: Range<Decoration>[] = [];
  for (const visible of view.visibleRanges) {
    const text = view.state.doc.sliceString(visible.from, visible.to);
    for (let index = 0; index < text.length; index++) {
      const character = text[index];
      if (character === " " || character === "\t") {
        ranges.push(whitespaceMark.range(visible.from + index, visible.from + index + 1));
      }
    }
  }
  return Decoration.set(ranges, true);
}
