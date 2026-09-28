import type { Editor } from "@tiptap/core";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import { Decoration, DecorationSet } from "@tiptap/pm/view";
import { closeHistory } from "@tiptap/pm/history";

export interface DictationDraft {
  update: (text: string) => void;
  finish: () => void;
  cancel: () => void;
}

const previews = new WeakMap<Editor, () => string>();

export function hasDictationPreview(editor: Editor): boolean {
  return previews.has(editor);
}

/** Draft storage must never include speech that the user can still cancel. */
export function getDraftMarkdown(editor: Editor): string {
  return previews.get(editor)?.() ?? editor.getMarkdown();
}

/** A read-only preview paragraph; surrounding edits keep their own positions. */
export function beginDictationDraft(editor: Editor): DictationDraft | null {
  if (editor.isDestroyed) return null;
  const paragraph = editor.schema.nodes.paragraph;
  const markdown = editor.markdown;
  if (!paragraph || !markdown || previews.has(editor)) return null;
  const key = new PluginKey("dictationDraft");
  let from = editor.state.doc.content.size;
  let to = from + 2;
  let active = true;
  editor.view.dispatch(closeHistory(editor.state.tr).insert(from, paragraph.create()));
  previews.set(editor, () => markdown.serialize(editor.state.tr.delete(from, to).doc.toJSON()));
  editor.registerPlugin(new Plugin({
    key,
    filterTransaction(transaction, state) {
      if (!transaction.docChanged || transaction.getMeta(key)) return true;
      const mappedFrom = transaction.mapping.map(from, 1);
      const mappedTo = transaction.mapping.map(to, -1);
      const original = state.doc.nodeAt(from);
      const next = transaction.doc.nodeAt(mappedFrom);
      return mappedTo > mappedFrom && !!original && !!next && original.eq(next);
    },
    state: {
      init: () => null,
      apply(transaction) {
        if (!transaction.getMeta(key)) {
          from = transaction.mapping.map(from, 1);
          to = transaction.mapping.map(to, -1);
        }
        return null;
      },
    },
    props: {
      decorations(state) {
        return DecorationSet.create(state.doc, [Decoration.node(from, to, {
          "data-dictation-preview": "",
          contenteditable: "false",
          "aria-readonly": "true",
          class: "rounded-sm bg-muted/60 text-muted-foreground",
        })]);
      },
    },
  }));
  const finish = () => {
    if (!active) return;
    active = false;
    previews.delete(editor);
    if (!editor.isDestroyed) {
      editor.unregisterPlugin(key);
      const transaction = closeHistory(editor.state.tr);
      editor.view.dispatch(transaction);
      editor.emit("update", { editor, transaction, appendedTransactions: [] });
    }
  };
  return {
    update(text) {
      if (!active || editor.isDestroyed) return;
      const node = paragraph.create(null, text ? editor.schema.text(text) : null);
      const transaction = editor.state.tr.replaceWith(from, to, node)
        .setMeta(key, true).setMeta("addToHistory", false);
      to = from + node.nodeSize;
      editor.view.dispatch(transaction);
    },
    finish,
    cancel() {
      if (!active || editor.isDestroyed) return;
      finish();
      editor.view.dispatch(editor.state.tr.delete(from, to).setMeta("addToHistory", false));
    },
  };
}
