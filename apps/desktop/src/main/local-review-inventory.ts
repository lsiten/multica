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
  const healthPayload = await transport.health(profile);
  // A null/failed health means the daemon is not reachable (e.g. during startup
  // or offline). Resolve to null so the renderer can fall back to the remote
  // worktree list instead of surfacing a raw "expected object, received null"
  // zod error from the IPC bridge.
  if (!healthPayload) return null;
  const health = localReviewRuntimeHealthSchema.parse(healthPayload);
  if (health.profile !== profile.name) throw new Error("Local worktree inventory profile mismatch");
  const worktrees = await transport.inventory(profile);
  parseManagedWorktrees(worktrees);
  return { health, worktrees };
}
