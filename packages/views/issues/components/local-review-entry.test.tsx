// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";
import { readReviewRepositories } from "../../platform/local-review-pages";
import { LocalReviewEntry } from "./local-review-entry";

vi.mock("@multica/core/auth", () => ({ useAuthStore: (select: (state: { user: { id: string } }) => unknown) => select({ user: { id: "viewer" } }) }));
vi.mock("../../platform/local-review-pages", () => ({ readReviewRepositories: vi.fn() }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });
const request = { task_id: "run", workspace_id: "ws", runtime_id: "runtime", path: "/workdir", target: "main" };
it("enables MR only after a recheck finds a repository", async () => {
  vi.mocked(readReviewRepositories).mockResolvedValueOnce({ repositories: [] }).mockResolvedValueOnce({ repositories: ["/workdir/repo"] });
  const open = vi.fn();
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewEntry request={request} onOpen={open} /></QueryClientProvider>, { locale: "zh-Hans" });
  await screen.findByText("无可审查的 Git 仓库");
  fireEvent.click(screen.getByRole("button", { name: "重新检查" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "查看 / 提交 MR" })).toBeEnabled());
  expect(open).not.toHaveBeenCalled();
});
it.each(["empty", "failed", "ready"])("checks %s repositories before allowing MR to open", async (state) => {
  if (state === "failed") vi.mocked(readReviewRepositories).mockRejectedValue(new Error("offline"));
  else vi.mocked(readReviewRepositories).mockResolvedValue({ repositories: state === "ready" ? ["/workdir/repo"] : [] });
  const open = vi.fn();
  renderWithI18n(<QueryClientProvider client={new QueryClient()}><LocalReviewEntry request={request} onOpen={open} /></QueryClientProvider>, { locale: "zh-Hans" });
  const button = screen.getByRole("button", { name: "查看 / 提交 MR" });
  expect(button).toBeDisabled();
  fireEvent.click(button);
  expect(open).not.toHaveBeenCalled();
  if (state === "ready") {
    await waitFor(() => expect(button).toBeEnabled());
    fireEvent.click(button);
    expect(open).toHaveBeenCalledWith(request);
  } else {
    await screen.findByText(state === "empty" ? "无可审查的 Git 仓库" : "无法验证仓库，请检查运行时连接后重试");
    expect(button).toBeDisabled();
    fireEvent.click(button);
    expect(open).not.toHaveBeenCalled();
  }
});
