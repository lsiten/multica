// @vitest-environment node
import { expect, it, vi } from "vitest";
import { requestReviewInventory } from "./local-review-inventory";

it("keeps the checked profile for the inventory request", async () => {
  const profile = { name: "desktop", port: 4567 };
  const inventory = vi.fn(async () => []);
  const health = { profile: "desktop", workspaces: [{ id: "ws", runtimes: ["runtime"] }] };
  const result = await requestReviewInventory({ resolveProfile: async () => profile, health: async () => health, inventory });
  expect(result).toEqual({ health, worktrees: [] });
  expect(inventory).toHaveBeenCalledWith(profile);
});

it("rejects mismatched profile identity before reading inventory", async () => {
  const inventory = vi.fn(async () => []);
  await expect(requestReviewInventory({ resolveProfile: async () => ({ name: "desktop", port: 4567 }), health: async () => ({ profile: "another", workspaces: [] }), inventory })).rejects.toThrow("profile mismatch");
  expect(inventory).not.toHaveBeenCalled();
});

it("rejects unavailable health rather than returning an empty inventory", async () => {
  await expect(requestReviewInventory({ resolveProfile: async () => ({ name: "desktop", port: 4567 }), health: async () => null, inventory: async () => [] })).rejects.toThrow();
});

it("rejects malformed inventory rather than claiming repositories were removed", async () => {
  await expect(requestReviewInventory({ resolveProfile: async () => ({ name: "desktop", port: 4567 }), health: async () => ({ profile: "desktop", workspaces: [] }), inventory: async () => ({ repositories: [] }) })).rejects.toThrow("Invalid worktree inventory response");
});
