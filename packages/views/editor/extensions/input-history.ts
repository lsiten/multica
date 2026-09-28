import { Extension } from "@tiptap/core";
import { Plugin, Selection, TextSelection } from "@tiptap/pm/state";
import { isImeComposing } from "@multica/core/utils";

export type InputHistoryDirection = "previous" | "next";

/** Open pickers consume arrows first; ordinary editing keeps every other key. */
export function createInputHistoryExtension(
  navigate: (direction: InputHistoryDirection, empty: boolean) => boolean,
) {
  return Extension.create({
    name: "inputHistory",
    // Pickers use 101. Added after the native keymaps at 100 so code-block
    // exit and other arrows only run when the composer declines navigation.
    priority: 100,
    addProseMirrorPlugins() {
      const editor = this.editor;
      return [new Plugin({
        props: {
          handleKeyDown(view, event) {
            if (
              (event.key !== "ArrowUp" && event.key !== "ArrowDown") ||
              event.defaultPrevented || event.altKey || event.ctrlKey ||
              event.metaKey || event.shiftKey || view.composing ||
              isImeComposing(event) || !editor.isEditable
            ) return false;
            const { selection, doc } = view.state;
            if (!(selection instanceof TextSelection) || !selection.empty) return false;
            // Native horizontal movement can reach the next keydown before
            // ProseMirror receives selectionchange. Never replace that caret.
            const domSelection = view.dom.ownerDocument.getSelection();
            if (domSelection?.anchorNode && view.dom.contains(domSelection.anchorNode) &&
              (!domSelection.isCollapsed ||
                view.posAtDOM(domSelection.anchorNode, domSelection.anchorOffset) !== selection.from)) {
              return false;
            }
            if (
              selection.from !== Selection.atStart(doc).from &&
              selection.from !== Selection.atEnd(doc).from
            ) return false;
            const empty = doc.childCount === 1 && doc.firstChild?.type.name === "paragraph" &&
              doc.firstChild.content.size === 0;
            if (!navigate(event.key === "ArrowUp" ? "previous" : "next", empty)) return false;
            editor.commands.setTextSelection(Selection.atEnd(editor.state.doc).from);
            editor.commands.scrollIntoView();
            return true;
          },
        },
      })];
    },
  });
}
