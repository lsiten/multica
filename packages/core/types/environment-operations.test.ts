// @vitest-environment node
import { expect, it } from "vitest";
import { environmentCommandSchema, environmentOperationBatches, parseEnvironmentPolicyStatus, parseEnvironmentOperationStatus } from "./environment-operations";

it("requires unique identifiers and reviewed mutations", () => {
  expect(() => environmentCommandSchema.parse({ action: "operation_start", operation: { id: "a".repeat(64), action: "clean_cache", selections: [{ environment_id: "b".repeat(64) }] } })).toThrow();
  expect(() => environmentCommandSchema.parse({ action: "operation_start", operation: { id: "a".repeat(64), action: "restore", selections: [], archive_id: "/" } })).toThrow();
});

it("validates policy updates and fails closed on malformed policy responses", () => {
  const policy = { enabled: true, archive_after_hours: 24, cache_after_hours: 12, pressure_cache_after_hours: 1, max_idle_environments: 100, max_directory_bytes: 20 * 1024 ** 3, minimum_free_bytes: 5 * 1024 ** 3 };
  expect(environmentCommandSchema.parse({ action: "policy_update", policy }).action).toBe("policy_update");
  expect(() => environmentCommandSchema.parse({ action: "policy_update", policy: { ...policy, archive_after_hours: -1 } })).toThrow();
  expect(() => parseEnvironmentPolicyStatus({ policy })).toThrow("Invalid environment policy response");
});

it("includes every selection in bounded batches rather than dropping entries after 1000", () => {
  const selections = Array.from({ length: 2501 }, (_, index) => ({ environment_id: index.toString(16).padStart(64, "0"), revision: "b".repeat(64) }));
  const batches = environmentOperationBatches("archive", selections);
  expect(batches.map((batch) => batch.selections.length)).toEqual([1000, 1000, 501]);
  expect(batches.flatMap((batch) => batch.selections)).toEqual(selections);
  expect(new Set(batches.map((batch) => batch.id)).size).toBe(3);
  expect(() => environmentOperationBatches("archive", [selections[0]!, selections[0]!])).toThrow("Unique");
});

it("preserves future operation states as unknown and rejects impossible progress", () => {
  const receipt = { id: "a".repeat(64), action: "archive", status: "future", workspace_id: "ws", runtime_id: "runtime", daemon_id: "daemon", profile: "profile", started_at: "2026-10-03T00:00:00Z", updated_at: "2026-10-03T00:00:00Z", completed: 0, total: 1, results: [], error: "" };
  expect(parseEnvironmentOperationStatus(receipt).status).toBe("unknown");
  expect(() => parseEnvironmentOperationStatus({ ...receipt, completed: 2 })).toThrow();
  expect(() => parseEnvironmentOperationStatus({ ...receipt, results: [{ reclaimed: "true" }] })).toThrow();
});
