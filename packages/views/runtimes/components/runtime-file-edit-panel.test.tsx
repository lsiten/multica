// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { ApiClient, setApiInstance } from "@multica/core/api";
import { useRuntimeFileEditDraftStore } from "@multica/core/runtimes";
import { renderWithI18n } from "../../test/i18n";
import { RuntimeFileEditPanel } from "./runtime-file-edit-panel";

const base = {
  taskId: "task-1",
  path: "/runtime/repo",
  target: "main",
  relativePath: "notes.txt",
  baseContent: "line one\n",
  baseSha256: "a".repeat(64),
};

function okResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    statusText: status === 200 ? "OK" : "",
    headers: { "Content-Type": "application/json" },
  });
}

// The api proxy only resolves to a real client once setApiInstance has run, so
// the test installs a client and mocks the global fetch it uses (the codebase's
// established pattern). A 409 makes fetchRaw throw ApiError; a 200 with
// conflict:true resolves to the runtime-wins result.
function renderPanel(
  props: Partial<Parameters<typeof RuntimeFileEditPanel>[0]> = {},
  fetchMock: ReturnType<typeof vi.fn> = vi.fn(),
) {
  setApiInstance(new ApiClient("https://api.example.test"));
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  function Providers({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  }
  renderWithI18n(
    <Providers>
      <RuntimeFileEditPanel {...base} isBinary={false} canEdit={true} isRemote={true} {...props} />
    </Providers>,
  );
  return client;
}

async function editThenSave(user: ReturnType<typeof userEvent.setup>, value: string) {
  await user.click(screen.getByRole("button", { name: "Edit" }));
  const editor = await screen.findByRole("textbox");
  await user.clear(editor);
  await user.type(editor, value);
  await user.click(screen.getByRole("button", { name: "Save" }));
}

beforeEach(() => {
  vi.clearAllMocks();
  useRuntimeFileEditDraftStore.setState({ drafts: {} });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("RuntimeFileEditPanel", () => {
  it("renders binary files read-only with no edit control", () => {
    renderPanel({ isBinary: true, baseContent: "" });
    expect(screen.queryByText(/Binary files can’t be edited here\.|二进制文件无法在此编辑/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });

  it("does not show edit for a file the user cannot edit", () => {
    renderPanel({ canEdit: false });
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });

  it("submits a remote three-way merge on save", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      okResponse({ edit: { file_path: "notes.txt", merged: true, bytes: 12, new_sha256: "b".repeat(64) } }),
    );
    renderPanel({}, fetchMock);
    const user = userEvent.setup();
    await editThenSave(user, "line one edited\n");
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const body = JSON.parse((fetchMock.mock.calls[0]?.[1] as { body?: string }).body ?? "{}");
    expect(body).toMatchObject({
      action: "merge_edit",
      relative_path: "notes.txt",
      edit_base: "line one\n",
      edit_new: "line one edited\n",
    });
  });

  it("reloads and reports a changed base on 409", async () => {
    const onReload = vi.fn();
    const fetchMock = vi.fn().mockResolvedValue(okResponse({ error: "working file changed" }, 409));
    renderPanel({ onReload }, fetchMock);
    const user = userEvent.setup();
    await editThenSave(user, "line one edited\n");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/changed|Reload|加载/);
    expect(onReload).toHaveBeenCalledTimes(1);
  });

  it("rebases onto the runtime version on a merge conflict", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      okResponse({
        edit: {
          file_path: "notes.txt",
          merged: false,
          conflict: true,
          bytes: 0,
          new_content: "runtime wins\n",
          current_sha256: "c".repeat(64),
        },
      }),
    );
    renderPanel({}, fetchMock);
    const user = userEvent.setup();
    await editThenSave(user, "line one edited\n");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/newer version|最新版本/);
    await waitFor(() =>
      expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe(
        "runtime wins\n",
      ),
    );
  });
});
