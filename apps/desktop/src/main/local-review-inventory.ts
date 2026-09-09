import { localReviewRuntimeHealthSchema } from "@multica/core/types/local-review";
import { parseManagedWorktrees } from "@multica/core/types/managed-worktree";

type Profile = { readonly name: string; readonly port: number };
interface InventoryTransport {
  resolveProfile(): Promise<Profile | null>;
  health(profile: Profile): Promise<unknown>;
  inventory(profile: Profile): Promise<unknown>;
}

export async function requestReviewInventory(transport: InventoryTransport) {
  const profile = await transport.resolveProfile();
  if (!profile) return null;
  const health = localReviewRuntimeHealthSchema.parse(await transport.health(profile));
  if (health.profile !== profile.name) throw new Error("Local worktree inventory profile mismatch");
  const worktrees = await transport.inventory(profile);
  parseManagedWorktrees(worktrees);
  return { health, worktrees };
}
