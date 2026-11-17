// @vitest-environment jsdom
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { ApiError } from "@multica/core/api";
import { useWorkspaceFileOperationStore } from "@multica/core/chat";
import type { WorkspaceFileOperationResult } from "@multica/core/types/workspace-file-operation";
import enChat from "../../locales/en/chat.json";
import enCommon from "../../locales/en/common.json";
import { requestReviewPage } from "../../platform/local-review-pages";
import { WorkspaceFileCreate } from "./workspace-file-create";

vi.mock("../../platform/local-review-pages", () => ({ requestReviewPage: vi.fn() }));
vi.mock("@multica/core/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@multica/core/api")>(),
  api: { getConfig: async () => ({ workspace_file_operations_supported: true }) },
}));
const context = { workspace_id: "workspace", runtime_id: "runtime", task_id: "task", path: "/work" };
beforeEach(() => {
  vi.clearAllMocks();
  useWorkspaceFileOperationStore.setState({ pending: {} });
});
function mount(supported = true) {
  const created = vi.fn();
  render(<QueryClientProvider client={new QueryClient()}><I18nProvider locale="en" resources={{ en: { chat: enChat, common: enCommon } }}><WorkspaceFileCreate context={context} supported={supported} onCreated={created} /></I18nProvider></QueryClientProvider>);
  return created;
}

it("retains the original pending creation when directory capability reads are unavailable", async () => {
  useWorkspaceFileOperationStore.getState().prepare(context, { kind: "create_file", path: "pending.md" });
  mount(false);

  await userEvent.setup().click(await screen.findByRole("button", { name: "Resume creation" }));

  expect(screen.getByLabelText("Relative path")).toHaveValue("pending.md");
  expect(screen.getByLabelText("Relative path")).toBeDisabled();
});

it("keeps the form during admission and a lost response, then completes only after retry confirms creation", async () => {
  let rejectRequest: (error: Error) => void = () => { throw new Error("Request not started"); };
  vi.mocked(requestReviewPage).mockImplementationOnce(() => new Promise((_, reject) => { rejectRequest = reject; }));
  const created = mount();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "New" }));
  await user.type(screen.getByLabelText("Relative path"), "notes.md");
  await user.click(screen.getByRole("button", { name: "Create" }));
  expect(screen.getByLabelText("Relative path")).toBeDisabled();
  expect(created).not.toHaveBeenCalled();
  await user.keyboard("{Escape}");
  expect(screen.getByRole("dialog")).toBeInTheDocument();

  await act(async () => rejectRequest(new Error("Connection lost")));
  expect(await screen.findByRole("alert")).toHaveTextContent("Could not create");
  expect(screen.getByLabelText("Relative path")).toHaveValue("notes.md");
  expect(screen.getByLabelText("Relative path")).toBeDisabled();
  vi.mocked(requestReviewPage).mockImplementationOnce(async (input) => ({ kind: "file_operation", command_id: input.command_id ?? "", operation: "create_file", path: "notes.md", directory: false }));
  await user.click(screen.getByRole("button", { name: "Retry" }));

  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  expect(created).toHaveBeenCalledWith(expect.objectContaining({ path: "notes.md", directory: false }));
  expect(useWorkspaceFileOperationStore.getState().pending).toEqual({});
});

it("allows correcting the name only after an explicit refusal without filesystem changes", async () => {
  vi.mocked(requestReviewPage).mockRejectedValueOnce(new ApiError("File exists", 409, "Conflict", { code: "workspace_file_operation_rejected" }));
  const created = mount();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "New" }));
  await user.selectOptions(screen.getByLabelText("Type"), "create_directory");
  await user.type(screen.getByLabelText("Relative path"), "existing");
  await user.click(screen.getByRole("button", { name: "Create" }));

  expect(await screen.findByRole("alert")).toHaveTextContent("Could not create");
  expect(screen.getByLabelText("Relative path")).toBeEnabled();
  expect(screen.getByLabelText("Relative path")).toHaveValue("existing");
  expect(created).not.toHaveBeenCalled();
  const receipt: WorkspaceFileOperationResult = { kind: "file_operation", command_id: "new-command", operation: "create_directory", path: "new-folder", directory: true };
  vi.mocked(requestReviewPage).mockResolvedValueOnce(receipt);
  await user.clear(screen.getByLabelText("Relative path"));
  await user.type(screen.getByLabelText("Relative path"), "new-folder");
  await user.click(screen.getByRole("button", { name: "Create" }));
  await waitFor(() => expect(created).toHaveBeenCalledWith(receipt));
});
