const guards = new Set<() => boolean>();
let approvalDepth = 0;

/** Register a guard for the currently mounted view, including native tab actions. */
export function registerNavigationGuard(guard: () => boolean): () => void {
  guards.add(guard);
  return () => { guards.delete(guard); };
}

export function mayLeaveNavigation(): boolean {
  return approvalDepth > 0 || [...guards].every(guard => guard());
}

/** Share one synchronous approval across an adapter and its underlying store. */
export function runGuardedNavigation(action: () => void, force = false): void {
  if (!force && !mayLeaveNavigation()) return;
  approvalDepth++;
  try { action(); } finally { approvalDepth--; }
}
