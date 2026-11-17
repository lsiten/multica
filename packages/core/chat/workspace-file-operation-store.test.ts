// @vitest-environment jsdom
import { beforeEach, expect, it } from "vitest";
import { useWorkspaceFileOperationStore, workspaceFileOperationScope } from "./workspace-file-operation-store";
import type { WorkspaceFileOperation } from "../types/workspace-file-operation";

const context = { workspace_id: "workspace", runtime_id: "runtime", task_id: "task", path: "/work/project" };
beforeEach(() => useWorkspaceFileOperationStore.setState({ pending: {} }));

it.each<WorkspaceFileOperation>([
  { kind: "create_file", path: "first.md" },
  { kind: "move", path: "first.md", destination: "moved.md", expected_file_id: "26d9d04d-5c45-40b6-bb58-c60e7d59433c" },
  { kind: "trash", path: "first.md", expected_file_id: "26d9d04d-5c45-40b6-bb58-c60e7d59433c" },
  { kind: "restore", path: "first.md", recovery_id: "original-deletion" },
])( "reuses the admitted command and payload after reload, while isolating a different run: $kind", async (operation) => {
  const original = useWorkspaceFileOperationStore.getState().prepare(context, operation);
  const persisted = localStorage.getItem("multica_workspace_file_creations");
  if (!persisted) throw new Error("Pending command was not persisted");
  useWorkspaceFileOperationStore.setState({ pending: {} });
  localStorage.setItem("multica_workspace_file_creations", persisted);
  await useWorkspaceFileOperationStore.persist.rehydrate();

  const retry = useWorkspaceFileOperationStore.getState().prepare(context, { kind: "create_directory", path: "another-path" });
  const otherRun = useWorkspaceFileOperationStore.getState().prepare({ ...context, task_id: "another-run" }, { kind: "create_directory", path: "another-path" });

  expect(retry.commandId).toBe(original.commandId);
  expect(retry.context).toEqual(context);
  expect(retry.operation).toEqual(operation);
  expect(otherRun.commandId).not.toBe(original.commandId);
});

it("keeps pending commands isolated by runtime and task identity", () => {
  const operation: WorkspaceFileOperation = { kind: "move", path: "notes.md", destination: "moved.md", expected_file_id: "26d9d04d-5c45-40b6-bb58-c60e7d59433c" };
  const original = useWorkspaceFileOperationStore.getState().prepare({ ...context, runtime_id: "old-runtime" }, operation);
  const rebound = useWorkspaceFileOperationStore.getState().prepare({ ...context, runtime_id: "new-runtime", task_id: "replacement-task" }, { kind: "trash", path: "other.md", expected_file_id: "26d9d04d-5c45-40b6-bb58-c60e7d59433c" });

  expect(rebound.commandId).not.toBe(original.commandId);
  expect(rebound.operation).not.toEqual(operation);
  expect(rebound.context).toEqual({ ...context, runtime_id: "new-runtime", task_id: "replacement-task" });
  expect(Object.keys(useWorkspaceFileOperationStore.getState().pending)).toHaveLength(2);
  expect(useWorkspaceFileOperationStore.getState().pending[workspaceFileOperationScope({ ...context, runtime_id: "old-runtime" })]?.commandId).toBe(original.commandId);
  expect(useWorkspaceFileOperationStore.getState().pending[workspaceFileOperationScope({ ...context, runtime_id: "new-runtime", task_id: "replacement-task" })]?.commandId).toBe(rebound.commandId);
});

it("does not let a late response clear another command or a different directory", () => {
  const state = useWorkspaceFileOperationStore.getState();
  const first = state.prepare(context, { kind: "create_directory", path: "notes" });
  state.settle(workspaceFileOperationScope(context), first.commandId);
  const next = state.prepare(context, { kind: "create_file", path: "next.md" });
  const otherContext = { ...context, workspace_id: "other-workspace" };
  const other = state.prepare(otherContext, { kind: "create_file", path: "other.md" });

  state.settle(workspaceFileOperationScope(context), first.commandId);

  expect(useWorkspaceFileOperationStore.getState().pending[workspaceFileOperationScope(context)]).toEqual(next);
  expect(useWorkspaceFileOperationStore.getState().pending[workspaceFileOperationScope(otherContext)]).toEqual(other);
});

it("discards invalid persisted commands without losing a valid sibling", async () => {
  const valid = useWorkspaceFileOperationStore.getState().prepare(context, { kind: "create_file", path: "note.md" });
  const scope = workspaceFileOperationScope(context);
  localStorage.setItem("multica_workspace_file_creations", JSON.stringify({ state: { pending: { [scope]: valid, broken: { ...valid, operation: { kind: "create_file", path: "../escape" } }, wrongScope: valid } }, version: 0 }));

  await useWorkspaceFileOperationStore.persist.rehydrate();

  expect(useWorkspaceFileOperationStore.getState().pending).toEqual({ [scope]: valid });
});

it("migrates the legacy workspace/path scope without dropping a retry", async () => {
  const operation: WorkspaceFileOperation = { kind: "trash", path: "legacy.md", expected_file_id: "26d9d04d-5c45-40b6-bb58-c60e7d59433c" };
  const pending = useWorkspaceFileOperationStore.getState().prepare(context, operation);
  const legacyScope = JSON.stringify([context.workspace_id, context.path]);
  useWorkspaceFileOperationStore.setState({ pending: {} });
  localStorage.setItem("multica_workspace_file_creations", JSON.stringify({ state: { pending: { [legacyScope]: pending } }, version: 0 }));
  await useWorkspaceFileOperationStore.persist.rehydrate();

  expect(useWorkspaceFileOperationStore.getState().pending[workspaceFileOperationScope(context)]).toEqual(pending);
});
