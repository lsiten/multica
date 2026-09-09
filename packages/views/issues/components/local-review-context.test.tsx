// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";
import { readReviewContext } from "../../platform/local-review-pages";
import { LocalReviewContext } from "./local-review-context";

vi.mock("../../platform/local-review-pages", () => ({ readReviewContext: vi.fn() }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

it("expands in bounded batches and stops at the hunk boundary", async () => {
  const version = "a".repeat(64);
  vi.mocked(readReviewContext).mockImplementation(async (input) => ({
    version_id: version, path: "a.ts", preview: "text",
    page: {
      lines: Array.from({ length: input.limit ?? 0 }, (_, index) => ({ text: `context-${(input.offset ?? 0) + index + 1}`, kind: "context", new_line: (input.offset ?? 0) + index + 1 })),
      next_line: (input.offset ?? 0) + (input.limit ?? 0), has_more: true,
    },
  }));
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewContext request={{ task_id: "task", workspace_id: "ws", path: "/repo", target: "main" }} versionId={version} filePath="a.ts" oldStart={10} newStart={20} count={120} /></QueryClientProvider>, { locale: "zh-Hans" });
  expect(readReviewContext).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "展开 50 行上下文" }));
  await screen.findByText("context-69");
  fireEvent.click(screen.getByRole("button", { name: "展开 50 行上下文" }));
  await screen.findByText("context-119");
  fireEvent.click(screen.getByRole("button", { name: "展开 20 行上下文" }));
  await screen.findByText("context-139");
  expect(readReviewContext).toHaveBeenLastCalledWith(expect.objectContaining({ action: "context", offset: 119, limit: 20, version_id: version }), expect.any(AbortSignal));
  expect(screen.queryByRole("button", { name: /展开.*行上下文/ })).not.toBeInTheDocument();
});
