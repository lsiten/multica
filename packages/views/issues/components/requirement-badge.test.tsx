import { screen } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useQuery } from "@tanstack/react-query";
import { RequirementBadge } from "./requirement-badge";

vi.mock("@tanstack/react-query", () => ({
  useQuery: vi.fn(),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/issues/queries", () => ({
  requirementOptions: (_wsId: string, issueId: string) => ({
    queryKey: ["requirement", issueId],
  }),
}));

const mockUseQuery = vi.mocked(useQuery);

describe("RequirementBadge", () => {
  beforeEach(() => {
    mockUseQuery.mockImplementation(() => ({
      data: null,
    }) as ReturnType<typeof useQuery>);
  });

  it("renders the requirement marker for a kind=requirement issue", () => {
    renderWithI18n(
      <RequirementBadge issue={{ id: "issue-1", kind: "requirement" } as never} />,
    );
    expect(screen.getByText("R")).toBeInTheDocument();
  });

  it("renders nothing for an ordinary (task) issue", () => {
    const { container } = renderWithI18n(
      <RequirementBadge issue={{ id: "issue-2", kind: "task" } as never} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("surfaces the confirmed lifecycle when asked", () => {
    mockUseQuery.mockImplementation(() => ({
      data: {
        issue_id: "issue-1",
        lifecycle_status: "confirmed",
        unconfirmed_change: false,
      },
    }) as ReturnType<typeof useQuery>);
    renderWithI18n(
      <RequirementBadge issue={{ id: "issue-1", kind: "requirement" } as never} showLifecycle />,
    );
    expect(screen.getByText("Confirmed")).toBeInTheDocument();
  });
});
