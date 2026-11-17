// @vitest-environment jsdom
import { beforeEach, expect, it } from "vitest";
import { useChatReadingPositionStore } from "./reading-position-store";

beforeEach(() => useChatReadingPositionStore.setState({ positions: {} }));

it("restores distinct conversation anchors and live-follow choices after reload", async () => {
  const first = JSON.stringify(["workspace", "first"]);
  const second = JSON.stringify(["workspace", "second"]);
  const anchor = { kind: "message", messageId: "older-answer", offset: 420, createdAt: "2026-11-16T03:00:00Z" } as const;
  useChatReadingPositionStore.getState().setPosition(first, anchor);
  useChatReadingPositionStore.getState().setPosition(second, { kind: "live" });
  const persisted = localStorage.getItem("multica_chat_reading_positions");
  if (!persisted) throw new Error("Reading position was not persisted");
  useChatReadingPositionStore.setState({ positions: {} });
  localStorage.setItem("multica_chat_reading_positions", persisted);

  await useChatReadingPositionStore.persist.rehydrate();

  expect(useChatReadingPositionStore.getState().positions).toEqual({ [first]: anchor, [second]: { kind: "live" } });
});

it("drops malformed positions without losing valid sibling bookmarks", async () => {
  localStorage.setItem("multica_chat_reading_positions", JSON.stringify({ state: { positions: {
    valid: { kind: "message", messageId: "answer", offset: -12 },
    unknown: { kind: "future-position", offset: 0 },
    malformed: { kind: "message", messageId: "answer", offset: "420" },
  } }, version: 0 }));

  await useChatReadingPositionStore.persist.rehydrate();

  expect(useChatReadingPositionStore.getState().positions).toEqual({ valid: { kind: "message", messageId: "answer", offset: -12 } });
});
