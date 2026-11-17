// @vitest-environment jsdom
import { useState } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { ApiError } from "@multica/core/api";
import { useWorkspaceFileOperationStore } from "@multica/core/chat";
import enChat from "../../locales/en/chat.json";
import enCommon from "../../locales/en/common.json";
import { requestReviewPage } from "../../platform/local-review-pages";
import { WorkspaceFileOperationDialog, type WorkspaceFileChoice } from "./workspace-file-operation-dialog";
import { WorkspaceFilePending } from "./workspace-file-pending";

vi.mock("../../platform/local-review-pages", () => ({ requestReviewPage: vi.fn() }));
const context = { workspace_id: "workspace", runtime_id: "runtime", task_id: "original-task", path: "/original-root" };
const fileId = "26d9d04d-5c45-40b6-bb58-c60e7d59433c";
beforeEach(() => { vi.resetAllMocks(); useWorkspaceFileOperationStore.setState({ pending: {} }); });
function mount(initial: WorkspaceFileChoice) {
  const completed = vi.fn();
  function Surface() {
    const [choice, setChoice] = useState<WorkspaceFileChoice | null>(initial);
    return <><WorkspaceFilePending wsId="workspace" onResume={(pending) => setChoice({ pending })} />{choice && <WorkspaceFileOperationDialog key={"pending" in choice ? choice.pending.commandId : "initial"} choice={choice} onClose={() => { completed(); setChoice(null); }} />}</>;
  }
  render(<QueryClientProvider client={new QueryClient()}><I18nProvider locale="en" resources={{ en: { chat: enChat, common: enCommon } }}><Surface /></I18nProvider></QueryClientProvider>);
  return completed;
}

it("resumes the original move after closing an unconfirmed operation and retains its selected identity", async () => {
  vi.mocked(requestReviewPage).mockRejectedValueOnce(new Error("Lost response"));
  const completed = mount({ context, directory: false, operation: { kind: "move", path: "original.md", destination: "original.md", expected_file_id: fileId } });
  const user = userEvent.setup();
  expect(screen.getByRole("button", { name: "Move or rename" })).toBeDisabled();
  await user.clear(screen.getByLabelText("Destination path"));
  await user.type(screen.getByLabelText("Destination path"), "moved.md");
  await user.click(screen.getByRole("button", { name: "Move or rename" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("unconfirmed");
  expect(screen.getByLabelText("Destination path")).toBeDisabled();
  const pending = Object.values(useWorkspaceFileOperationStore.getState().pending)[0];
  expect(pending?.operation).toEqual({ kind: "move", path: "original.md", destination: "moved.md", expected_file_id: fileId });
  expect(completed).not.toHaveBeenCalled();
  await user.click(screen.getByText("Close", { selector: "button" }));
  await user.click(screen.getByRole("button", { name: "Resume operation" }));
  expect(screen.getByLabelText("Destination path")).toHaveValue("moved.md");
  expect(screen.getByText("/original-root")).toBeVisible();
  vi.mocked(requestReviewPage).mockImplementationOnce(async (input) => ({ kind: "file_operation", command_id: input.command_id ?? "", operation: "move", path: "original.md", destination: "moved.md", directory: false, file_id: fileId }));

  await user.click(screen.getByRole("button", { name: "Retry" }));

  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  expect(useWorkspaceFileOperationStore.getState().pending).toEqual({});
  expect(requestReviewPage).toHaveBeenLastCalledWith(expect.objectContaining({ task_id: "original-task", path: "/original-root", command_id: pending?.commandId, file_operation: pending?.operation }));
});

it("keeps a folder deletion confirmation open until the server confirms its receipt", async () => {
  let resolve: (value: Awaited<ReturnType<typeof requestReviewPage>>) => void = () => { throw new Error("No deletion started"); };
  vi.mocked(requestReviewPage).mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
  const completed = mount({ context, directory: true, operation: { kind: "trash", path: "folder", expected_file_id: fileId } });
  const user = userEvent.setup();
  expect(screen.getByRole("alertdialog")).toHaveTextContent("everything inside");
  expect(requestReviewPage).not.toHaveBeenCalled();

  await user.click(screen.getByRole("button", { name: "Move to trash" }));
  await user.keyboard("{Escape}");

  expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
  expect(completed).not.toHaveBeenCalled();
  const pending = Object.values(useWorkspaceFileOperationStore.getState().pending)[0];
  await act(async () => resolve({ kind: "file_operation", command_id: pending?.commandId ?? "", operation: "trash", path: "folder", directory: true, recovery_id: pending?.commandId }));
  await waitFor(() => expect(completed).toHaveBeenCalledOnce());
});

it("keeps a refused restore editable so the member can choose a nonconflicting path", async () => {
  vi.mocked(requestReviewPage).mockRejectedValueOnce(new ApiError("Destination exists", 409, "Conflict", { code: "workspace_file_operation_rejected" }));
  const completed = mount({ context, directory: false, operation: { kind: "restore", path: "original.md", recovery_id: "original-delete" } });
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Restore" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Could not complete");
  expect(screen.getByLabelText("Destination path")).toBeEnabled();
  expect(completed).not.toHaveBeenCalled();
  vi.mocked(requestReviewPage).mockImplementationOnce(async (input) => ({ kind: "file_operation", command_id: input.command_id ?? "", operation: "restore", path: "recovered.md", directory: false, recovery_id: "original-delete" }));
  await user.clear(screen.getByLabelText("Destination path"));
  await user.type(screen.getByLabelText("Destination path"), "recovered.md");

  await user.click(screen.getByRole("button", { name: "Restore" }));

  await waitFor(() => expect(completed).toHaveBeenCalledOnce());
  expect(useWorkspaceFileOperationStore.getState().pending).toEqual({});
});
