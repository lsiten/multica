import { describe, expect, it } from "vitest";
import { NotificationBotListSchema } from "./schema";

describe("notification bot API boundary", () => {
  it("maps valid wire fields and strips credentials", () => {
    // Given
    const wire = {
      available: true,
      bots: [
        {
          id: "fixture",
          name: "Team",
          platform: "wecom",
          is_enabled: false,
          credentials: "never expose",
        },
      ],
    };
    // When
    const parsed = NotificationBotListSchema.parse(wire);
    // Then
    expect(parsed.bots[0]).toEqual({
      id: "fixture",
      name: "Team",
      platform: "wecom",
      isEnabled: false,
      lastDeliveryAt: null,
      lastError: "",
    });
  });
  it.each([
    {},
    { available: true, bots: [{}] },
    {
      available: true,
      bots: [
        { id: "fixture", name: "Team", platform: "unknown", is_enabled: true },
      ],
    },
  ])("rejects incomplete or unknown server contracts", (wire) => {
    // When / Then
    expect(NotificationBotListSchema.safeParse(wire).success).toBe(false);
  });
});
