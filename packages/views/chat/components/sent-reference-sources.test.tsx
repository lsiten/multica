import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { composeChatReferences } from "@multica/core/chat";
import { renderWithI18n } from "../../test/i18n";
import { SentReferenceSources } from "./sent-reference-sources";

const source = { workspaceId: "workspace", resource: { kind: "issue", id: "proposal" }, revision: 7, state: "saved", extent: "selection" } as const;
const content = composeChatReferences("Check this", [{ id: "reference", label: "Proposal", content: "Original quoted paragraph", source }]);

describe("sent document reference sources", () => {
  it("opens the source with the quoted revision while leaving message content intact", () => {
    const open = vi.fn();
    renderWithI18n(<SentReferenceSources content={content} workspaceId="workspace" onOpen={open} />);
    fireEvent.click(screen.getByRole("button", { name: "Proposal Saved · v7" }));
    expect(open).toHaveBeenCalledExactlyOnceWith({ label: "Proposal", ...source });
  });

  it("keeps a source from another workspace readable without opening it in this workspace", () => {
    const open = vi.fn();
    renderWithI18n(<SentReferenceSources content={content} workspaceId="other" onOpen={open} />);
    const button = screen.getByRole("button", { name: "Proposal Saved · v7" });
    expect(button).toBeDisabled();
    fireEvent.click(button);
    expect(open).not.toHaveBeenCalled();
  });

  it("opens the original message with its conversation identity", () => {
    const messageSource = { workspaceId: "workspace", sessionId: "original-chat", messageId: "original-message", role: "assistant", createdAt: "2026-11-15T13:00:00Z" } as const;
    const open = vi.fn();
    const quoted = composeChatReferences("Continue", [{ id: "quote", label: "Original answer", content: "Original answer snapshot", messageSource }]);
    renderWithI18n(<SentReferenceSources content={quoted} workspaceId="workspace" onOpenMessage={open} />);
    fireEvent.click(screen.getByRole("button", { name: "Original answer" }));
    expect(open).toHaveBeenCalledExactlyOnceWith({ label: "Original answer", ...messageSource });
  });
});
