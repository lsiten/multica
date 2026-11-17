// @vitest-environment node

import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { DocumentCommentContractError, type DocumentComment } from "./document-comments";

const resource = { kind: "issue", id: "d2e2a74c-1b1d-44ea-8bd8-e838ba3f46ab" } as const;
const comment: DocumentComment = {
  id: "7b99f651-ea52-4bf6-b122-3fc980b97684", resource, parent_id: null,
  anchor: { quote: "Quoted paragraph", start: 40, prefix: "Before", suffix: "After", revision: 3 },
  content: "Clarify this paragraph", author_id: "08cd892e-2e6c-46fc-8017-a64d3de2b5a2",
  resolved_at: null, resolved_by: null, revision: 1,
  created_at: "2026-11-15T00:00:00Z", updated_at: "2026-11-15T00:00:00Z",
};

function respond(body: unknown) {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } })));
  return new ApiClient("https://api.example.test").documentComments;
}
afterEach(() => vi.unstubAllGlobals());

describe("document comment receipts", () => {
  it("keeps the source selection and supplies defaults for older optional discussion fields", async () => {
    const api = respond({ comments: [{ ...comment, resolved_at: undefined, resolved_by: undefined, parent_id: undefined }], next_offset: 1 });

    const result = await api.list("workspace", resource, 0);

    expect(result.comments[0]?.anchor).toEqual({ quote: "Quoted paragraph", start: 40, prefix: "Before", suffix: "After", revision: 3 });
    expect(result.comments[0]?.resolved_at).toBeNull();
    expect(result.has_more).toBe(false);
  });

  it.each(["read", "create", "resolve"] as const)("rejects a malformed %s receipt instead of reporting a successful empty result", async (operation) => {
    const api = respond({ id: 42, comments: "not a page" });
    const operations = {
      read: () => api.list("workspace", resource, 0),
      create: () => api.create("workspace", { resource, content: "Keep this unsent note" }),
      resolve: () => api.resolve("workspace", { resource, comment, resolved: true }),
    };

    await expect(operations[operation]()).rejects.toBeInstanceOf(DocumentCommentContractError);
  });

  it("rejects a successful response for another discussion", async () => {
    const api = respond({ ...comment, id: "38798f0e-774d-4530-b9b4-e9b08c4a3b3d", resolved_at: "2026-11-15T01:00:00Z" });

    await expect(api.resolve("workspace", { resource, comment, resolved: true })).rejects.toBeInstanceOf(DocumentCommentContractError);
  });

  it("does not accept a reply attached to a different thread", async () => {
    const api = respond({ ...comment, parent_id: "38798f0e-774d-4530-b9b4-e9b08c4a3b3d" });

    await expect(api.create("workspace", { resource, content: "Reply", parent_id: comment.id })).rejects.toBeInstanceOf(DocumentCommentContractError);
  });
});
