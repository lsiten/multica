// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { NotificationBot } from "@multica/core/notification-bots/schema";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";
import { NotificationBotEditor } from "./notification-bot-editor";

const existing: NotificationBot = {
  id: "fixture",
  name: "Private bot",
  platform: "telegram",
  isEnabled: true,
  lastError: "",
  lastDeliveryAt: null,
};
function setup(bot: NotificationBot | null) {
  const onSave = vi.fn();
  render(
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, settings: enSettings } }}
    >
      <NotificationBotEditor
        bot={bot}
        pending={false}
        onSave={onSave}
        onClose={vi.fn()}
      />
    </I18nProvider>,
  );
  return onSave;
}

describe("notification bot editor", () => {
  it("preserves credentials when editing public fields", async () => {
    // Given
    const onSave = setup(existing);
    const user = userEvent.setup();
    // When
    await user.clear(screen.getByLabelText("Bot name"));
    await user.type(screen.getByLabelText("Bot name"), "Renamed");
    await user.click(screen.getByRole("button", { name: "Save" }));
    // Then
    expect(onSave).toHaveBeenCalledWith({
      id: "fixture",
      name: "Renamed",
      platform: "telegram",
      isEnabled: true,
    });
    expect(screen.queryByLabelText("Bot token")).toBeNull();
  });

  it("requires replacement credentials when explicitly replacing them", async () => {
    // Given
    const onSave = setup(existing);
    const user = userEvent.setup();
    // When
    await user.click(
      screen.getByRole("switch", { name: "Replace credentials" }),
    );
    await user.type(screen.getByLabelText("Bot token"), "123:fixture");
    await user.type(screen.getByLabelText("Chat ID"), "-100123");
    await user.click(screen.getByRole("button", { name: "Save" }));
    // Then
    expect(onSave).toHaveBeenCalledWith({
      id: "fixture",
      name: "Private bot",
      platform: "telegram",
      isEnabled: true,
      credentials: {
        webhookURL: "",
        secret: "",
        botToken: "123:fixture",
        chatID: "-100123",
      },
    });
  });
});
