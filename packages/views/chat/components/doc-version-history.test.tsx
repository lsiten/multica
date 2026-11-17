import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { DocumentContentVersion } from "@multica/core/api/document-content-version";
import { renderWithI18n } from "../../test/i18n";
import { DocVersionHistory } from "./doc-version-history";

const original: DocumentContentVersion = {
  id: "8be14c73-23a2-459d-aef0-c883eb7429b9", owner_type: "issue",
  owner_id: "07951141-ffeb-450b-b0f5-e10c356553ba", revision: 1,
  content_type: "text/markdown", content: "Original paragraph", content_url: null,
  size_bytes: 18, actor_type: "member", actor_id: "9fdc5700-c9c7-499c-b4e7-9aa85f44db5a",
  merged: false, note: null, created_at: "2026-11-15T00:00:00Z",
};

function setup(onRollback: (revision: number) => Promise<void>, options?: { onAddToChat?: (content: string, revision: number) => void; content?: string }) {
  const current = { ...original, id: "43a95416-4554-4d04-9fe9-18db35f44a32", revision: 2, content: options?.content ?? "Edited paragraph" };
  renderWithI18n(<DocVersionHistory versions={[current, original]} current={current} loading={false}
    onRollback={onRollback} onDelete={vi.fn()} rollbackLabel="Restore"
    diffLabel="Compare" rollbackConfirmTitle="Restore this version?"
    rollbackConfirmDescription="Creates a new current version." cancelLabel="Cancel" onAddToChat={options?.onAddToChat} />);
  fireEvent.click(screen.getByRole("button", { name: "Restore" }));
  return screen.getByRole("alertdialog");
}

describe("restoring a document version", () => {
  it("keeps confirmation open and prevents duplicate actions until the server accepts", async () => {
    const pending = Promise.withResolvers<void>();
    const restore = vi.fn().mockReturnValue(pending.promise);
    const dialog = setup(restore);
    fireEvent.click(within(dialog).getByRole("button", { name: "Restore" }));
    expect(dialog).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Restore" })).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(restore).toHaveBeenCalledExactlyOnceWith(1);
    await act(async () => { pending.resolve(); await pending.promise; });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  });

  it("retains the selected revision and lets a failed restore retry", async () => {
    const restore = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValue(undefined);
    const dialog = setup(restore);
    fireEvent.click(within(dialog).getByRole("button", { name: "Restore" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Restore was not confirmed");
    fireEvent.click(within(dialog).getByRole("button", { name: "Restore" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(restore.mock.calls).toEqual([[1], [1]]);
  });

  it("quotes the selected historical revision and describes the change from current to restored content", () => {
    const quote = vi.fn();
    const dialog = setup(vi.fn(), { onAddToChat: quote, content: "Original paragraph\nSecond paragraph\nThird paragraph" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.getByText(/Restoring creates a new version/)).toHaveTextContent("+0 / -2");
    fireEvent.click(screen.getByRole("button", { name: "Use this version in chat" }));
    expect(quote).toHaveBeenCalledExactlyOnceWith("Original paragraph", 1);
  });
});
