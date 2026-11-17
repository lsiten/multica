// @vitest-environment jsdom

import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import type { Issue } from "../types";
import { issueKeys } from "../issues/queries";
import { useDocumentCurrentContent } from "./document-current-content";
import { useRecordDocumentContentVersion, useRollbackDocumentContentVersion } from "./document-content-version-queries";

vi.mock("../api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api")>(),
  api: { getIssue: vi.fn(), recordIssueContentVersion: vi.fn(), rollbackIssueContentVersion: vi.fn() },
}));

const issue: Issue = {
  id: "issue-1", workspace_id: "ws-1", number: 1, identifier: "MUL-1",
  title: "New requirement", description: "Initial body without history", revision: 7,
  status: "todo", priority: "none", assignee_type: null, assignee_id: null,
  creator_type: "member", creator_id: "member-1", parent_issue_id: null,
  project_id: null, position: 1, stage: null, start_date: null, due_date: null,
  metadata: {}, properties: {}, created_at: "2026-11-15T00:00:00Z", updated_at: "2026-11-15T00:00:00Z",
};

function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  return { client, ...renderHook(() => ({
    current: useDocumentCurrentContent("ws-1", { kind: "issue", id: issue.id }),
    record: useRecordDocumentContentVersion("ws-1"),
    restore: useRollbackDocumentContentVersion("ws-1"),
  }), { wrapper }) };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getIssue).mockResolvedValue(issue);
});

describe("current document content", () => {
  it("reads a new issue body and its write revision before any checkpoint exists", async () => {
    const { result } = setup();

    await waitFor(() => expect(result.current.current.data?.content).toBe("Initial body without history"));

    expect(result.current.current.data?.owner_revision).toBe(7);
  });

  it("updates the document when the canonical issue cache receives another member's edit", async () => {
    const { client, result } = setup();
    await waitFor(() => expect(result.current.current.data?.owner_revision).toBe(7));

    client.setQueryData(issueKeys.detail("ws-1", issue.id), { ...issue, description: "Another member's edit", revision: 8 });

    await waitFor(() => expect(result.current.current.data?.content).toBe("Another member's edit"));
    expect(result.current.current.data?.owner_revision).toBe(8);
  });

  it.each(["save", "restore"] as const)("refreshes the visible body after a successful %s", async (action) => {
    const { result } = setup();
    await waitFor(() => expect(result.current.current.data?.owner_revision).toBe(7));
    const updated = { ...issue, description: "Committed document body", revision: 8 };
    vi.mocked(api.getIssue).mockResolvedValue(updated);
    const version = {
      id: "version-2", owner_type: "issue", owner_id: issue.id, revision: 2, owner_revision: 8,
      content: updated.description, content_url: null, content_type: "text/markdown", size_bytes: 23,
      actor_type: "member", actor_id: "member-1", merged: false, note: null, created_at: "2026-11-15T01:00:00Z",
    } as const;
    vi.mocked(api.recordIssueContentVersion).mockResolvedValue(version);
    vi.mocked(api.rollbackIssueContentVersion).mockResolvedValue(version);

    await act(async () => {
      switch (action) {
        case "save":
          await result.current.record.mutateAsync({ ref: { kind: "issue", id: issue.id }, input: { content_type: "text/markdown", content: updated.description, expected_revision: 7 } });
          break;
        case "restore":
          await result.current.restore.mutateAsync({ ref: { kind: "issue", id: issue.id }, revision: 1 });
          break;
        default: {
          const unhandled: never = action;
          throw unhandled;
        }
      }
    });

    await waitFor(() => expect(result.current.current.data?.content).toBe("Committed document body"));
    expect(result.current.current.data?.owner_revision).toBe(8);
  });
});
