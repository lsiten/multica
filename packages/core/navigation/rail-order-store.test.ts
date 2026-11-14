import { beforeEach, describe, expect, it } from "vitest";
import { RAIL_SPACES } from "../paths/spaces";
import { resolveRailOrder, useRailOrderStore } from "./rail-order-store";

describe("resolveRailOrder", () => {
  it("uses the default order until the user reorders", () => {
    expect(resolveRailOrder(null)).toEqual(RAIL_SPACES);
  });

  it("adds Applications to navigation saved before its entry existed", () => {
    expect(resolveRailOrder(["chat", "inbox", "work", "team", "autopilots"])).toEqual([
      "chat",
      "inbox",
      "applications",
      "work",
      "team",
      "autopilots",
    ]);
  });

  it("keeps a saved order", () => {
    const saved = ["chat", "inbox", "applications", "work", "team", "autopilots"];
    expect(resolveRailOrder(saved)).toEqual(saved);
  });

  it("drops unknown and repeated ids", () => {
    expect(resolveRailOrder(["chat", "settings", 3, "chat", "inbox", "applications", "work", "team", "autopilots"])).toEqual([
      "chat",
      "inbox",
      "applications",
      "work",
      "team",
      "autopilots",
    ]);
  });

  it("puts a space missing from the saved order after its default neighbour", () => {
    expect(resolveRailOrder(["chat", "inbox", "applications", "autopilots", "work"])).toEqual([
      "chat",
      "inbox",
      "applications",
      "autopilots",
      "work",
      "team",
    ]);
    expect(resolveRailOrder(["chat", "work", "team", "autopilots"])).toEqual([
      "inbox",
      "applications",
      "chat",
      "work",
      "team",
      "autopilots",
    ]);
  });
});

describe("useRailOrderStore", () => {
  beforeEach(() => {
    useRailOrderStore.setState({ order: null });
  });

  it("saves a custom order", () => {
    const order = ["chat", "inbox", "applications", "work", "team", "autopilots"] as const;
    useRailOrderStore.getState().setOrder(order);
    expect(useRailOrderStore.getState().order).toEqual(order);
  });

  it("forgets the order once it is back to the default", () => {
    useRailOrderStore.getState().setOrder(["work", "inbox", "applications", "team", "chat", "autopilots"]);
    useRailOrderStore.getState().setOrder(RAIL_SPACES);
    expect(useRailOrderStore.getState().order).toBeNull();
  });
});
