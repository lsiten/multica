/** Failure reasons whose task session can safely be resumed by the chat UI. */
const RESUMABLE_FAILURE_REASONS = new Set([
  "timeout",
  "runtime_offline",
  "runtime_recovery",
  "skill_bundle_unavailable",
  "agent_error.provider_network",
  "agent_error.provider_capacity_or_rate_limit",
  "agent_error.provider_server_error",
]);

/**
 * Keep this list deliberately explicit. Unknown and poisoned failures should
 * offer a fresh retry only; a resume button must never suggest that a broken
 * provider session can be continued safely.
 */
export function canContinueChatFailure(reason: string | null | undefined): boolean {
  return typeof reason === "string" && RESUMABLE_FAILURE_REASONS.has(reason);
}
