// @vitest-environment jsdom
import { beforeEach, expect, it } from "vitest";
import { useWorkspaceFileEditStore } from "./workspace-file-edit-store";

const document = { kind: "workspace_file", id: "notes", fileId: "66f46490-15d2-43ca-bdbb-89a18617dd9e" } as const;
const request = { workspace_id: "workspace", task_id: "task", path: "/root", relative_path: "notes.md", target: "HEAD", action: "merge_edit", edit_base: "original", edit_new: "submitted", file_id: document.fileId } as const;
beforeEach(() => useWorkspaceFileEditStore.setState({ pending: {} }));

it("retries the original file edit after reload even when a newer draft or path is supplied", async () => {
  const original = useWorkspaceFileEditStore.getState().prepare(document, request);
  const persisted = localStorage.getItem("multica_workspace_file_edits");
  if (!persisted) throw new Error("Edit was not persisted");
  useWorkspaceFileEditStore.setState({ pending: {} });
  localStorage.setItem("multica_workspace_file_edits", persisted);
  await useWorkspaceFileEditStore.persist.rehydrate();

  const retry = useWorkspaceFileEditStore.getState().prepare(document, { ...request, relative_path: "moved.md", edit_new: "newer draft" });

  expect(retry).toEqual(original);
  expect(retry.request.edit_new).toBe("submitted");
});

it("keeps a replacement's command separate and ignores a late settlement for an earlier save", () => {
  const state = useWorkspaceFileEditStore.getState();
  const first = state.prepare(document, request);
  state.settle(first);
  const next = state.prepare(document, { ...request, edit_new: "next" });
  const replacement = { ...document, fileId: "f89b7499-3ad7-4dcc-8d70-9630b5313bc7" };
  const other = state.prepare(replacement, { ...request, file_id: replacement.fileId });

  state.settle(first);

  expect(Object.values(useWorkspaceFileEditStore.getState().pending)).toEqual([next, other]);
  expect(other.request.command_id).not.toBe(next.request.command_id);
});
