// @vitest-environment node
import { expect, it } from "vitest";
import { applicationRevisionDiff } from "./revision-diff";
import { defaultApplicationConfig } from "./schema";

it("compares credential changes without exposing their values", () => {
  const before = { config: { ...defaultApplicationConfig(), environment: { TOKEN: "old-secret", REMOVED: "removed-secret" } }, relations: [] };
  const after = { config: { ...before.config, environment: { TOKEN: "new-secret", ADDED: "added-secret" } }, relations: [] };
  const changes = applicationRevisionDiff(before, after);
  expect(changes).toEqual([
    { path: "environment.ADDED", before: null, after: "••••••", sensitive: true },
    { path: "environment.REMOVED", before: "••••••", after: null, sensitive: true },
    { path: "environment.TOKEN", before: "••••••", after: "••••••", sensitive: true },
  ]);
  expect(JSON.stringify(changes)).not.toMatch(/old-secret|new-secret|added-secret|removed-secret/);
});

it("ignores record and relationship ordering but preserves command order", () => {
  const first = { source_id: "source", target_id: "one", type: "depends_on" as const, required: true, condition: "healthy" as const, start_external: false };
  const second = { ...first, target_id: "two" };
  const before = { config: { ...defaultApplicationConfig(), command: ["node", "server.js"], environment: { FIRST: "1", SECOND: "2" }, connections: [{ target_id: "one", url_variable: "API" }, { target_id: "two", url_variable: "CACHE" }] }, relations: [first, second] };
  const after = { config: { ...before.config, environment: { SECOND: "2", FIRST: "1" }, connections: [...before.config.connections].reverse() }, relations: [second, first] };
  expect(applicationRevisionDiff(before, after)).toEqual([]);
  expect(applicationRevisionDiff(before, { ...after, config: { ...after.config, command: ["server.js", "node"] } })).toEqual([{ path: "command", before: '["node","server.js"]', after: '["server.js","node"]', sensitive: false }]);
});

it("reports health policy, dependency and local credential reference changes", () => {
  const config = defaultApplicationConfig();
  expect(applicationRevisionDiff({ config, relations: [] }, { config: { ...config, health: { ...config.health, path: "/healthz" }, local_env: { TOKEN: "HOST_TOKEN" } }, relations: [] })).toEqual([
    { path: "health.path", before: "", after: "/healthz", sensitive: false },
    { path: "local_env.TOKEN", before: null, after: "HOST_TOKEN", sensitive: false },
  ]);
});
