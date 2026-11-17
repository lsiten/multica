// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { applyReviewFileEdit } from "./local-review-pages";

const api = vi.hoisted(() => ({ applyRuntimeFileEdit: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api }));
afterEach(() => { vi.unstubAllGlobals(); vi.resetAllMocks(); });
const request = { task_id: "task", workspace_id: "workspace", path: "/root", target: "HEAD", relative_path: "notes.md", command_id: "save", action: "merge_edit", edit_base: "old", edit_new: "new" } as const;

it.each(["local", "relay"] as const)("refuses to acknowledge an unconfirmed %s file edit", async (transport) => {
  vi.stubGlobal("daemonAPI", transport === "local" ? { applyFileEdit: async () => ({ edit: null }) } : {});
  api.applyRuntimeFileEdit.mockResolvedValue(null);

  await expect(applyReviewFileEdit(request)).rejects.toThrow("File edit result is unconfirmed");
});

it("uses the server's file identity receipt even when desktop IPC can edit that path", async () => {
  const fileId = "66f46490-15d2-43ca-bdbb-89a18617dd9e";
  const local = vi.fn().mockResolvedValue({ file_path: "notes.md", conflict: false });
  vi.stubGlobal("daemonAPI", { applyFileEdit: local });
  const receipt = { file_path: "moved.md", command_id: "save", operation_id: "f89b7499-3ad7-4dcc-8d70-9630b5313bc7", file_id: fileId, conflict: false, merged: true, bytes: 3 };
  api.applyRuntimeFileEdit.mockResolvedValue(receipt);

  expect(await applyReviewFileEdit({ ...request, file_id: fileId })).toEqual(receipt);
  expect(local).not.toHaveBeenCalled();
});

it("does not acknowledge a file edit receipt for a different generation", async () => {
  vi.stubGlobal("daemonAPI", {});
  api.applyRuntimeFileEdit.mockResolvedValue({ file_path: "notes.md", command_id: "save", file_id: "f89b7499-3ad7-4dcc-8d70-9630b5313bc7", operation_id: "f89b7499-3ad7-4dcc-8d70-9630b5313bc7", conflict: false, merged: true, bytes: 3 });

  await expect(applyReviewFileEdit({ ...request, file_id: "66f46490-15d2-43ca-bdbb-89a18617dd9e" })).rejects.toThrow("File edit result is unconfirmed");
});
