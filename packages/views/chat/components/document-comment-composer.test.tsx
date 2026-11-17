import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@multica/core/api";
import { documentCommentScopeKey, useDocumentCommentDraftStore, type DocumentComment } from "@multica/core/chat";
import { renderWithI18n } from "../../test/i18n";
import { DocumentCommentComposer } from "./document-comment-composer";

vi.mock("@multica/core/api", async (original) => ({
  ...await original<typeof import("@multica/core/api")>(),
  api: { documentComments: { create: vi.fn() } },
}));

const resource = { kind: "issue", id: "07951141-ffeb-450b-b0f5-e10c356553ba" } as const;
const submissionId = "43a95416-4554-4d04-9fe9-18db35f44a32";
const parentId = "8be14c73-23a2-459d-aef0-c883eb7429b9";
const scope = documentCommentScopeKey("workspace", resource);
const response: DocumentComment = {
  id: submissionId, resource, content: "Unsent annotation", parent_id: null, anchor: null,
  author_id: "9fdc5700-c9c7-499c-b4e7-9aa85f44db5a", resolved_at: null, resolved_by: null,
  revision: 1, created_at: "2026-11-15T00:00:00Z", updated_at: "2026-11-15T00:00:00Z",
};

function setup(options?: { parentId?: string; onSubmitted?: () => void }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return renderWithI18n(<QueryClientProvider client={client}><DocumentCommentComposer wsId="workspace" resource={resource} {...options} /></QueryClientProvider>);
}

beforeEach(() => {
  useDocumentCommentDraftStore.setState({ drafts: {} });
  useDocumentCommentDraftStore.getState().setDraft(scope, { content: "Unsent annotation", submissionId });
  vi.mocked(api.documentComments.create).mockReset();
});

describe("document annotation composer", () => {
  it("keeps a failed note and its retry identity, then clears it after an accepted retry", async () => {
    vi.mocked(api.documentComments.create).mockRejectedValueOnce(new TypeError("Network unavailable")).mockResolvedValue(response);
    setup();

    fireEvent.click(screen.getByRole("button", { name: "Post annotation" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Your draft is saved");
    expect(screen.getByRole("textbox")).toHaveValue("Unsent annotation");
    expect(useDocumentCommentDraftStore.getState().drafts[scope]?.submissionId).toBe(submissionId);
    fireEvent.click(screen.getByRole("button", { name: "Post annotation" }));

    await waitFor(() => expect(screen.getByRole("textbox")).toHaveValue(""));
    expect(api.documentComments.create).toHaveBeenCalledTimes(2);
  });

  it("keeps a reply typed while the earlier reply is posting and leaves its form open", async () => {
    const replyScope = documentCommentScopeKey("workspace", resource, parentId);
    useDocumentCommentDraftStore.getState().setDraft(replyScope, { content: "Unsent annotation", submissionId });
    const pending = Promise.withResolvers<DocumentComment>();
    vi.mocked(api.documentComments.create).mockReturnValue(pending.promise);
    const onSubmitted = vi.fn();
    setup({ parentId, onSubmitted });
    fireEvent.click(screen.getByRole("button", { name: "Post reply" }));
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Next reply typed while waiting" } });

    await act(async () => { pending.resolve({ ...response, parent_id: parentId }); await pending.promise; });

    await waitFor(() => expect(screen.getByRole("button", { name: "Post reply" })).not.toBeDisabled());
    expect(screen.getByRole("textbox")).toHaveValue("Next reply typed while waiting");
    expect(onSubmitted).not.toHaveBeenCalled();
  });

  it("ignores a composing modifier-Enter and accepts the confirmed shortcut", async () => {
    vi.mocked(api.documentComments.create).mockResolvedValue(response);
    setup();
    const input = screen.getByRole("textbox");

    fireEvent.keyDown(input, { key: "Enter", ctrlKey: true, isComposing: true });
    expect(api.documentComments.create).not.toHaveBeenCalled();
    fireEvent.keyDown(input, { key: "Enter", ctrlKey: true });

    await waitFor(() => expect(input).toHaveValue(""));
  });
});
