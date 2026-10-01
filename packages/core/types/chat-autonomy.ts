export interface ChatAutonomyPolicyOverride {
  readonly mode: "autonomous";
  readonly max_duration_seconds: number;
  readonly max_token_count: number;
  /** Provider-reported execution cost cap in 1e-10 USD ticks; 0 means unlimited. */
  readonly max_cost_usd_ticks?: number;
}
