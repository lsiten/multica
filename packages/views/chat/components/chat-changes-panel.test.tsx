// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enChat from "../../locales/en/chat.json";
import { ChatChangesPanel } from "./chat-changes-panel";

const review = vi.hoisted(() => ({
  readReviewVersions: vi.fn(),
  readReviewManifest: vi.fn(),
  readLocalIndex: vi.fn(),
  changeLocalIndex: vi.fn(),
  renewReviewLease: vi.fn(),
}));

vi.mock("../../platform/local-review-pages", () => review);
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign((selector?: (state: { user: null }) => unknown) => selector ? selector({ user: null }) : { user: null }, { getState: () => ({ user: null }) }),
}));

const request = {
  task_id: "task-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  path: "/repo",
  target: "main",
};

beforeEach(() => {
  vi.clearAllMocks();
  review.readReviewVersions.mockResolvedValue({ versions: [{ version_id: "a".repeat(64), updated_at: "2026-11-16T10:00:00.000Z", header: { repository: "/repo", branch: "feature", target: "main", head: "b".repeat(40), target_head: "c".repeat(40), base: "d".repeat(40), dirty: true, committed: false } }] });
  review.readReviewManifest.mockResolvedValue({ version_id: "a".repeat(64), header: { repository: "/repo", branch: "feature", target: "main", head: "b".repeat(40), target_head: "c".repeat(40), base: "d".repeat(40), dirty: true, committed: false }, page: { files: [{ path: "src/app.ts", status: "modified", additions: 2, deletions: 1 }], total_files: 1, next_offset: 1, has_more: false, additions: 2, deletions: 1 }, review: { snapshot_id: "a".repeat(64), state: "draft", comment: "", merged_commit: "", events: [] } });
});

function renderPanel() {
  return render(<QueryClientProvider client={new QueryClient()}><I18nProvider locale="en" resources={{ en: { chat: enChat } }}><ChatChangesPanel request={request} onChanged={vi.fn()} onInvalidBinding={vi.fn()} /></I18nProvider></QueryClientProvider>);
}

describe("ChatChangesPanel", () => {
  it("loads and displays a selected review version without changing the current-run path", async () => {
    renderPanel();
    fireEvent.click(await screen.findByRole("tab", { name: "Selected version" }));
    expect(await screen.findByText("src/app.ts")).toBeInTheDocument();
    expect(review.readReviewManifest).toHaveBeenCalledWith(expect.objectContaining({ scope: "version", version_id: "a".repeat(64), target: "main" }), expect.any(AbortSignal));
    expect(review.readReviewVersions).toHaveBeenCalledWith(expect.objectContaining({ action: "versions" }), expect.any(AbortSignal));
  });
});
