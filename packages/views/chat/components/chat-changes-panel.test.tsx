// @vitest-environment jsdom
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PagedReviewInput } from "@multica/core/types/local-review-pages";
import { renderWithI18n } from "../../test/i18n";
import { reviewFileFixture, reviewManifestFixture } from "../../test/local-review-pages";
import { ChatChangesPanel } from "./chat-changes-panel";

const pages = vi.hoisted(() => ({ readReviewRepositories: vi.fn(), readGitBranches: vi.fn(), readGitManifest: vi.fn(), readReviewManifest: vi.fn(), readReviewFile: vi.fn(), readGitHistory: vi.fn(), readLocalIndex: vi.fn() }));
vi.mock("../../platform/local-review-pages", () => pages);
const request = { task_id: "task", workspace_id: "ws", runtime_id: "runtime", path: "/repo", target: "main" };
const index = { kind: "index", version_id: "a".repeat(64), unstaged_version_id: "b".repeat(64), staged_version_id: "c".repeat(64), status: { branch: "feature", head: "1".repeat(40), index_id: "d".repeat(64), files: [{ path: "entry.ts", unstaged: true, staged: false }] } };
function panel(input = request) { return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}><ChatChangesPanel request={input} onChanged={vi.fn()} onInvalidBinding={vi.fn()} /></QueryClientProvider>; }
beforeEach(() => {
  vi.clearAllMocks();
  pages.readReviewRepositories.mockImplementation(async (input: PagedReviewInput) => ({ repositories: [input.path] }));
  pages.readGitBranches.mockResolvedValue({ branches: ["main", "release"] });
  pages.readLocalIndex.mockResolvedValue(index);
  pages.readReviewManifest.mockImplementation(async (input: PagedReviewInput) => reviewManifestFixture(input));
  pages.readGitManifest.mockImplementation(async (input: PagedReviewInput) => reviewManifestFixture(input));
  pages.readReviewFile.mockImplementation(async (input: PagedReviewInput) => reviewFileFixture(input, `bytes:${input.path}`));
  pages.readGitHistory.mockResolvedValue({ commits: [{ sha: "9".repeat(40), subject: "Selected change" }], next_offset: 1, has_more: false });
});
afterEach(cleanup);

describe("workspace Git panel", () => {
  it("defaults to uncommitted read-only diff with original owning identity", async () => {
    renderWithI18n(panel());
    expect(await screen.findByText("bytes:/repo")).toBeInTheDocument();
    expect(pages.readLocalIndex).toHaveBeenCalledWith(expect.objectContaining({ ...request, scope: "checkout" }), expect.any(AbortSignal));
    expect(pages.readReviewFile).toHaveBeenCalledWith(expect.objectContaining({ scope: "checkout", path: "/repo", runtime_id: "runtime" }), expect.any(AbortSignal));
    expect(screen.getByRole("button", { name: "Uncommitted" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.queryByRole("button", { name: /commit changes|submit|approve|merge|stage file/i })).not.toBeInTheDocument();
    expect(pages.readGitManifest).not.toHaveBeenCalled();
  });
  it("compares only returned refs and opens selected commit diff", async () => {
    renderWithI18n(panel());
    fireEvent.click(await screen.findByRole("button", { name: "Branch diff" }));
    await waitFor(() => expect(pages.readGitManifest).toHaveBeenCalledWith(expect.objectContaining({ scope: "checkout", git_scope: "branch", target: "main" }), expect.any(AbortSignal)));
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "release" } });
    await waitFor(() => expect(pages.readGitManifest).toHaveBeenCalledWith(expect.objectContaining({ target: "release" }), expect.any(AbortSignal)));
    fireEvent.click(screen.getByRole("button", { name: "Commits" }));
    expect(await screen.findByRole("button", { name: /Selected change/ })).toBeInTheDocument();
    await waitFor(() => expect(pages.readGitManifest).toHaveBeenCalledWith(expect.objectContaining({ git_scope: "commit", commit_sha: "9".repeat(40), target: "" }), expect.any(AbortSignal)));
  });
  it("ignores a delayed repository response after switching directories", async () => {
    let finish: ((value: typeof index) => void) | undefined;
    pages.readLocalIndex.mockImplementation((input: PagedReviewInput) => input.path === "/repo" ? new Promise<typeof index>((resolve) => { finish = resolve; }) : Promise.resolve(index));
    const view = renderWithI18n(panel());
    view.rerender(panel({ ...request, path: "/other", runtime_id: "other-runtime" }));
    expect(await screen.findByText("bytes:/other")).toBeInTheDocument();
    await act(async () => { finish?.(index); });
    expect(screen.queryByText("bytes:/repo")).not.toBeInTheDocument();
    expect(screen.getByText("other-runtime")).toBeInTheDocument();
  });
  it("shows read errors without stale files", async () => {
    pages.readLocalIndex.mockRejectedValue(new Error("runtime offline"));
    renderWithI18n(panel());
    expect(await screen.findByRole("alert")).toHaveTextContent("runtime offline");
    expect(screen.queryByText("bytes:/repo")).not.toBeInTheDocument();
  });
});

it("switches repositories without carrying a diff or comparison target", async () => {
  pages.readReviewRepositories.mockResolvedValue({ repositories: ["/repo", "/other"] });
  renderWithI18n(panel());
  expect(await screen.findByText("bytes:/repo")).toBeInTheDocument();
  fireEvent.change(screen.getByRole("combobox", { name: "Repository" }), { target: { value: "/other" } });
  expect(await screen.findByText("bytes:/other")).toBeInTheDocument();
  expect(screen.queryByText("bytes:/repo")).not.toBeInTheDocument();
  expect(pages.readLocalIndex).toHaveBeenCalledWith(expect.objectContaining({ path: "/other", target: "", scope: "checkout" }), expect.any(AbortSignal));
});

it("reads commit history without a comparison branch", async () => {
  pages.readGitBranches.mockResolvedValue({ branches: [] });
  renderWithI18n(panel());
  fireEvent.click(await screen.findByRole("button", { name: "Commits" }));
  expect(await screen.findByRole("button", { name: /Selected change/ })).toBeInTheDocument();
  expect(pages.readGitHistory).toHaveBeenCalledWith(expect.objectContaining({ scope: "checkout" }), expect.any(AbortSignal));
});

it.each([
  ["directory runtime is offline", "The runtime is offline"],
  ["directory runtime unavailable", "you no longer have access"],
  ["checkout directory is unavailable", "work directory is no longer available"],
  ["select the repository root", "No Git repository"],
])("distinguishes %s", async (message, visible) => {
  pages.readReviewRepositories.mockRejectedValue(new Error(message));
  renderWithI18n(panel());
  expect(await screen.findByRole("alert")).toHaveTextContent(visible);
  expect(pages.readLocalIndex).not.toHaveBeenCalled();
});
