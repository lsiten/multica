import { afterEach, describe, expect, it } from "vitest";
import { Editor } from "@tiptap/core";
import { createEditorExtensions } from "./extensions";
import { beginDictationDraft, getDraftMarkdown, hasDictationPreview } from "./dictation-draft";

let editor: Editor | undefined;
function makeEditor(content = "Existing **draft**") {
  editor = new Editor({
    element: document.createElement("div"),
    extensions: createEditorExtensions({}),
    content,
    contentType: "markdown",
  });
  return editor;
}
afterEach(() => { editor?.destroy(); });

describe("dictation preview (real editor)", () => {
  it("omits live speech from persisted drafts while retaining manual edits", () => {
    const editor = makeEditor();
    const draft = beginDictationDraft(editor);
    draft?.update("unconfirmed speech");
    expect(editor.getText()).toContain("unconfirmed speech");
    expect(getDraftMarkdown(editor)).toBe("Existing **draft**");
    editor.commands.insertContentAt(1, "Typed ");
    expect(getDraftMarkdown(editor)).toBe("Typed Existing **draft**");
    draft?.cancel();
    expect(hasDictationPreview(editor)).toBe(false);
    expect(getDraftMarkdown(editor)).toBe("Typed Existing **draft**");
  });

  it("publishes final speech when the preview is accepted", () => {
    const editor = makeEditor();
    const draft = beginDictationDraft(editor);
    draft?.update("accepted speech");
    expect(getDraftMarkdown(editor)).not.toContain("accepted speech");
    const updates: string[] = [];
    editor.on("update", () => updates.push(getDraftMarkdown(editor)));
    draft?.finish();
    expect(updates).toEqual(["Existing **draft**\n\naccepted speech"]);
  });

  it("replaces partial snapshots and commits literal text without parsing markdown", () => {
    const editor = makeEditor();
    const draft = beginDictationDraft(editor);
    draft?.update("hello");
    draft?.update("# hello **world** <script> ");
    expect(editor.state.doc.lastChild?.type.name).toBe("paragraph");
    expect(editor.state.doc.lastChild?.textContent).toBe("# hello **world** <script> ");
    expect(editor.state.doc.lastChild?.firstChild?.marks).toEqual([]);
    expect(editor.state.doc.childCount).toBe(2);
    expect(editor.view.dom.querySelector("[data-dictation-preview]")?.getAttribute("contenteditable")).toBe("false");
    draft?.finish();
    expect(editor.view.dom.querySelector("[data-dictation-preview]")).toBeNull();
    editor.commands.insertContentAt(editor.state.doc.content.size - 1, " corrected");
    expect(editor.state.doc.lastChild?.textContent).toContain(" corrected");
    draft?.update("late result");
    expect(editor.getText()).not.toContain("late result");
  });

  it("preserves edits and formatting before the preview on correction and cancellation", () => {
    const editor = makeEditor();
    const draft = beginDictationDraft(editor);
    draft?.update("spoken");
    editor.commands.insertContentAt(1, "Typed ");
    draft?.update("spoken correction");
    expect(editor.state.doc.firstChild?.textContent).toBe("Typed Existing draft");
    draft?.cancel();
    expect(editor.getMarkdown()).toBe("Typed Existing **draft**");
    expect(editor.state.doc.childCount).toBe(1);
  });

  it("preserves content appended after the preview", () => {
    const editor = makeEditor();
    const draft = beginDictationDraft(editor);
    draft?.update("spoken");
    editor.commands.insertContentAt(editor.state.doc.content.size, {
      type: "paragraph", content: [{ type: "text", text: "upload.txt", marks: [{ type: "link", attrs: { href: "https://example.com/upload.txt" } }] }],
    });
    draft?.update("corrected");
    draft?.cancel();
    expect(editor.getMarkdown()).toContain("[upload.txt](https://example.com/upload.txt)");
    expect(editor.getText()).not.toContain("corrected");
  });

  it("protects the read-only preview from manual replacement", () => {
    const editor = makeEditor();
    const draft = beginDictationDraft(editor);
    draft?.update("spoken");
    const before = editor.getJSON();
    editor.commands.insertContentAt(editor.state.doc.content.size - 1, "should not enter preview");
    expect(editor.getJSON()).toEqual(before);
    draft?.finish();
    editor.commands.insertContentAt(editor.state.doc.content.size - 1, " allowed");
    expect(editor.getText()).toContain("spoken allowed");
  });

  it("removes an empty preview without changing an empty draft", () => {
    const editor = makeEditor("");
    beginDictationDraft(editor)?.cancel();
    expect(editor.isEmpty).toBe(true);
    expect(editor.state.doc.childCount).toBe(1);
  });

  it("lets undo remove the finished dictation in one operation", () => {
    const editor = makeEditor();
    const draft = beginDictationDraft(editor);
    draft?.update("partial");
    draft?.update("complete sentence");
    draft?.finish();
    editor.commands.undo();
    expect(editor.getMarkdown()).toBe("Existing **draft**");
  });
});
