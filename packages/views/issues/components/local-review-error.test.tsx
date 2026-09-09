// @vitest-environment jsdom
import { cleanup, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { renderWithI18n } from "../../test/i18n";
import { LocalReviewError, reviewErrorKind } from "./local-review-error";

afterEach(cleanup);

it("presents an oversized IPC response as an actionable error with collapsed details", () => {
  const error = new Error("Error invoking remote method 'daemon:read-local-review': Error: review output exceeds 8 MiB; narrow the changes before reviewing");
  renderWithI18n(<LocalReviewError error={error} id="review-error" />, { locale: "zh-Hans" });
  expect(screen.getByRole("alert")).toHaveTextContent("改动内容超过 8 MiB，暂时无法预览");
  expect(screen.getByText("技术详情").closest("details")).not.toHaveAttribute("open");
  expect(screen.queryByText(/Error invoking remote method/)).not.toBeInTheDocument();
});

it.each([
  ["target must be an existing local branch", "target"],
  ["Error invoking remote method 'daemon:read-local-review': Error: target must be an existing local branch", "target"],
  ["API error: 504", "timeout"],
  ["unexpected failure", "unknown"],
])("classifies %s without exposing the IPC wrapper", (message, expected) => {
  expect(reviewErrorKind(new Error(message))).toBe(expected);
});
