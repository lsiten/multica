"use client";

import { useEffect, useRef } from "react";
import { basicSetup } from "codemirror";
import { Compartment, EditorState } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { LanguageDescription } from "@codemirror/language";
import { languages } from "@codemirror/language-data";
import { basicSetupSearch } from "./code-mirror-search";

const editorTheme = EditorView.theme({
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
    minHeight: "100%",
    padding: "1rem 1.25rem 5rem",
  },
  ".cm-line": {
    padding: "0 .25rem",
  },
  ".cm-gutters": {
    backgroundColor: "transparent",
    color: "var(--muted-foreground)",
    border: "none",
  },
  ".cm-activeLine": {
    backgroundColor: "color-mix(in oklab, var(--muted) 52%, transparent)",
  },
  ".cm-activeLineGutter": {
    backgroundColor: "transparent",
  },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground": {
    backgroundColor:
      "color-mix(in oklab, var(--primary) 22%, transparent)",
  },
  "&.cm-focused": {
    outline: "none",
  },
});

export function CodeMirrorEditor({
  value,
  path,
  label,
  readOnly = false,
  autoFocus = false,
  onChange,
  onSave,
  onCheckpoint,
  onExit,
  onCursorChange,
}: {
  readonly value: string;
  readonly path: string;
  readonly label: string;
  readonly readOnly?: boolean;
  readonly autoFocus?: boolean;
  readonly onChange?: (value: string) => void;
  readonly onSave?: () => void;
  readonly onCheckpoint?: () => void;
  readonly onExit?: () => void;
  readonly onCursorChange?: (position: {
    readonly line: number;
    readonly column: number;
  }) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  const onSaveRef = useRef(onSave);
  const onCheckpointRef = useRef(onCheckpoint);
  const onExitRef = useRef(onExit);
  const onCursorChangeRef = useRef(onCursorChange);
  const readOnlyCompartment = useRef(new Compartment());
  const languageCompartment = useRef(new Compartment());
  const lastValue = useRef(value);

  useEffect(() => {
    onChangeRef.current = onChange;
  }, [onChange]);

  useEffect(() => {
    onSaveRef.current = onSave;
  }, [onSave]);

  useEffect(() => {
    onCheckpointRef.current = onCheckpoint;
  }, [onCheckpoint]);

  useEffect(() => {
    onExitRef.current = onExit;
  }, [onExit]);

  useEffect(() => {
    onCursorChangeRef.current = onCursorChange;
  }, [onCursorChange]);

  useEffect(() => {
    const parent = host.current;
    if (!parent) return;
    let disposed = false;
    const state = EditorState.create({
      doc: value,
      extensions: [
        basicSetup,
        basicSetupSearch,
        editorTheme,
        EditorView.lineWrapping,
        EditorView.contentAttributes.of({
          "aria-label": label,
          spellcheck: readOnly ? "false" : "true",
        }),
        readOnlyCompartment.current.of([
          EditorState.readOnly.of(readOnly),
          EditorView.editable.of(!readOnly),
        ]),
        languageCompartment.current.of([]),
        keymap.of([
          {
            key: "Mod-Shift-s",
            preventDefault: true,
            run: () => {
              onCheckpointRef.current?.();
              return true;
            },
          },
          {
            key: "Mod-s",
            preventDefault: true,
            run: () => {
              onSaveRef.current?.();
              return true;
            },
          },
          {
            key: "Escape",
            run: () => {
              onExitRef.current?.();
              return true;
            },
          },
        ]),
        EditorView.updateListener.of((update) => {
          if (update.docChanged) {
            const next = update.state.doc.toString();
            lastValue.current = next;
            onChangeRef.current?.(next);
          }
          if (update.docChanged || update.selectionSet) {
            const head = update.state.selection.main.head;
            const line = update.state.doc.lineAt(head);
            onCursorChangeRef.current?.({
              line: line.number,
              column: head - line.from + 1,
            });
          }
        }),
      ],
    });
    const editor = new EditorView({ state, parent });
    view.current = editor;

    const language = LanguageDescription.matchFilename(languages, path);
    if (language) {
      void language
        .load()
        .then((support) => {
          if (disposed || view.current !== editor) return;
          editor.dispatch({
            effects: languageCompartment.current.reconfigure(
              support.support,
            ),
          });
        })
        .catch(() => undefined);
    }
    if (autoFocus) editor.focus();

    return () => {
      disposed = true;
      if (view.current === editor) view.current = null;
      editor.destroy();
    };
    // The editor owns its document after mount. External value changes are
    // applied below without rebuilding the view and losing undo history.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [label, path]);

  useEffect(() => {
    const editor = view.current;
    if (!editor || value === lastValue.current) return;
    const current = editor.state.doc.toString();
    if (current === value) return;
    editor.dispatch({
      changes: { from: 0, to: editor.state.doc.length, insert: value },
    });
    lastValue.current = value;
  }, [value]);

  useEffect(() => {
    view.current?.dispatch({
      effects: readOnlyCompartment.current.reconfigure([
        EditorState.readOnly.of(readOnly),
        EditorView.editable.of(!readOnly),
      ]),
    });
  }, [readOnly]);

  useEffect(() => {
    if (autoFocus) view.current?.focus();
  }, [autoFocus]);

  return <div ref={host} className="h-full min-h-0" />;
}
