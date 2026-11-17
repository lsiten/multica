// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { ChatSessionProject } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enChat from "../../locales/en/chat.json";
import { ChatProjectSelector } from "./chat-project-selector";

it("shows unavailable projects as disabled choices", async () => {
  const projects: ChatSessionProject[] = [
    { id: "associated", workspace_id: "ws", title: "Associated", role: "associated", selectable: true },
    { id: "unavailable", workspace_id: "ws", title: "Unavailable", role: "unavailable", selectable: false },
  ];
  render(
    <I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
      <ChatProjectSelector projects={projects} selectedProjectId="associated" selectionRequired={false} onSelect={vi.fn()} />
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("combobox"));
  expect(await screen.findByRole("option", { name: "Unavailable" })).toHaveAttribute("aria-disabled", "true");
});
