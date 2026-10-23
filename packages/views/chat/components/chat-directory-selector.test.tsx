// @vitest-environment jsdom
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChatFileDirectory } from "@multica/core/chat";
import enChat from "../../locales/en/chat.json";
import { ChatDirectorySelector } from "./chat-directory-selector";

it("keeps other project directories collapsed and reveals them in batches", async () => {
  const selected: ChatFileDirectory = { key: "current", path: "/current", scope: "current_chat" };
  const directories: ChatFileDirectory[] = [
    selected,
    ...Array.from({ length: 21 }, (_, index) => ({
      key: `peer-${index}`,
      path: `/peer-${index}`,
      scope: "project_task" as const,
    })),
  ];
  render(
    <I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
      <ChatDirectorySelector directories={directories} selected={selected} onSelect={() => {}} />
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("combobox"));
  expect(await screen.findByRole("option", { name: /\/current/ })).toBeTruthy();
  expect(screen.queryByRole("option", { name: /\/peer-/ })).toBeNull();
  fireEvent.keyDown(screen.getByRole("listbox"), { key: "Escape" });
  fireEvent.click(screen.getByRole("button", { name: /Other task directories/ }));
  fireEvent.click(screen.getByRole("combobox"));
  expect(await screen.findByRole("option", { name: /\/peer-19$/ })).toBeTruthy();
  expect(screen.queryByRole("option", { name: /\/peer-20$/ })).toBeNull();
  fireEvent.keyDown(screen.getByRole("listbox"), { key: "Escape" });
  fireEvent.click(screen.getByRole("button", { name: "Load more" }));
  fireEvent.click(screen.getByRole("combobox"));
  expect(await screen.findByRole("option", { name: /\/peer-20$/ })).toBeTruthy();
  fireEvent.keyDown(screen.getByRole("listbox"), { key: "Escape" });
  fireEvent.click(screen.getByRole("button", { name: /Other task directories/ }));
  fireEvent.click(screen.getByRole("combobox"));
  expect(screen.queryByRole("option", { name: /\/peer-/ })).toBeNull();
});
