import { afterEach, describe, expect, it, vi } from "vitest";
import { Editor, Extension, type AnyExtension, type Content } from "@tiptap/core";
import { Plugin, Selection } from "@tiptap/pm/state";
import StarterKit from "@tiptap/starter-kit";
import CodeBlockLowlight from "@tiptap/extension-code-block-lowlight";
import { Markdown } from "@tiptap/markdown";
import { createInputHistoryExtension } from "./input-history";
import { codeLowlight } from "../syntax-highlight";

const editors: Editor[] = [];

function makeEditor(content: Content = "", extra: AnyExtension[] = []) {
  const navigate = vi.fn(() => true);
  const element = document.createElement("div");
  document.body.append(element);
  const editor = new Editor({
    element,
    content,
    parseOptions: { preserveWhitespace: "full" },
    extensions: [
      StarterKit.configure({ codeBlock: false, gapcursor: false }),
      CodeBlockLowlight.configure({ lowlight: codeLowlight }),
      Markdown, createInputHistoryExtension(navigate), ...extra,
    ],
  });
  editors.push(editor);
  return { editor, navigate };
}

function press(editor: Editor, options: KeyboardEventInit = {}) {
  const event = new KeyboardEvent("keydown", { key: "ArrowUp", cancelable: true, ...options });
  let handled = false;
  editor.view.someProp("handleKeyDown", (handler) => {
    if (!handler(editor.view, event)) return false;
    handled = true;
    return true;
  });
  return handled;
}

afterEach(() => {
  editors.splice(0).forEach((editor) => editor.destroy());
  document.body.replaceChildren();
});

describe("composer history arrows", () => {
  it("navigates at either full-document boundary and places the caret at the end", () => {
    const { editor, navigate } = makeEditor("<p>first line</p><p>last line</p>");
    editor.commands.setTextSelection(1);
    expect(press(editor)).toBe(true);
    expect(navigate).toHaveBeenCalledWith("previous", false);
    expect(editor.state.selection.from).toBe(Selection.atEnd(editor.state.doc).from);
    expect(press(editor, { key: "ArrowDown" })).toBe(true);
    expect(navigate).toHaveBeenLastCalledWith("next", false);
  });

  it.each([
    "<p>   </p>", "<p><br></p>", "<p></p><p></p>", "<h1></h1>",
  ])("does not report %s as an empty draft", (content) => {
    const { editor, navigate } = makeEditor(content);
    press(editor);
    expect(navigate).toHaveBeenCalledWith("previous", false);
  });

  it("reports only the initial empty paragraph as empty", () => {
    const { editor, navigate } = makeEditor();
    press(editor);
    expect(navigate).toHaveBeenCalledWith("previous", true);
  });

  it("leaves interior cursors and selections to the editor", () => {
    const { editor, navigate } = makeEditor("<p>first</p><p>last</p>");
    editor.commands.setTextSelection(3);
    press(editor);
    editor.commands.setTextSelection({ from: 1, to: 3 });
    press(editor);
    expect(navigate).not.toHaveBeenCalled();
  });

  it.each([
    { shiftKey: true }, { ctrlKey: true }, { metaKey: true }, { altKey: true },
    { isComposing: true }, { keyCode: 229 },
  ])("preserves modified arrows and IME: %j", (options) => {
    const { editor, navigate } = makeEditor();
    press(editor, options);
    expect(navigate).not.toHaveBeenCalled();
  });

  it("declines when native cursor movement precedes ProseMirror selectionchange", () => {
    const { editor, navigate } = makeEditor("<p>hello</p>");
    editor.commands.setTextSelection(6);
    const text = editor.view.dom.querySelector("p")?.firstChild;
    if (!text) throw new TypeError("Missing editor text node");
    document.getSelection()?.collapse(text, 4);
    expect(editor.state.selection.from).toBe(6);
    press(editor);
    expect(navigate).not.toHaveBeenCalled();
  });

  it("lets an open picker consume arrows first", () => {
    const picker = Extension.create({
      name: "historyTestPicker",
      priority: 101,
      addProseMirrorPlugins: () => [new Plugin({ props: { handleKeyDown: () => true } })],
    });
    const { editor, navigate } = makeEditor("", [picker]);
    expect(press(editor)).toBe(true);
    expect(navigate).not.toHaveBeenCalled();
  });

  it("offers history before the code-block exit arrow", () => {
    const { editor, navigate } = makeEditor();
    editor.commands.setContent("<pre><code>console.log(1)</code></pre>");
    editor.commands.setTextSelection(Selection.atEnd(editor.state.doc).from);
    editor.view.focus();
    navigate.mockImplementation(() => {
      editor.commands.setContent("");
      return true;
    });
    expect(press(editor, { key: "ArrowDown" })).toBe(true);
    expect(navigate).toHaveBeenCalledWith("next", false);
    expect(editor.getText()).toBe("");
  });

  it("preserves native arrows when history declines or editing is disabled", () => {
    const { editor, navigate } = makeEditor();
    navigate.mockReturnValue(false);
    expect(press(editor)).toBe(false);
    editor.setEditable(false);
    navigate.mockClear();
    press(editor);
    expect(navigate).not.toHaveBeenCalled();
  });
});
