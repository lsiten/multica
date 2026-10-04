// @vitest-environment node
import { expect, it } from "vitest";
import { parseEnvironmentRuntimes } from "./environment-operations";
import { environmentRuntimeAPIURL, environmentRuntimeLabel, groupEnvironmentRuntimeMachines } from "./environment-runtime-machines";

const runtime = { id: "codex", workspace_id: "ws", owner_id: "user", name: "Codex (eric)", custom_name: "eric", daemon_id: "host-1", provider: "codex", status: "online", runtime_mode: "local" };

it("groups only matching daemon and ownership identities, preserving every runtime", () => {
  const rows = parseEnvironmentRuntimes([
    runtime, { ...runtime, id: "claude", provider: "claude", status: "offline" },
    { ...runtime, id: "other-host", daemon_id: "host-2" },
    { ...runtime, id: "other-workspace", workspace_id: "other" },
    { ...runtime, id: "other-owner", owner_id: "other" },
    { ...runtime, id: "legacy-1", daemon_id: null }, { ...runtime, id: "legacy-2", daemon_id: null },
    { ...runtime, id: "host-1", daemon_id: null },
    { ...runtime, id: "cloud-worker", runtime_mode: "cloud" },
  ]);
  const machines = groupEnvironmentRuntimeMachines(rows);
  expect(machines).toHaveLength(8);
  const machine = machines.find((item) => item.runtimes.some((row) => row.id === "codex"))!;
  expect(machine.title).toBe("eric");
  expect(machine.runtimes.map((row) => row.id)).toEqual(["codex", "claude"]);
  expect(environmentRuntimeLabel(machine.runtimes[0]!, machine.title)).toBe("codex");
});

it("retains host metadata and tolerates older and malformed optional metadata", () => {
  const [current, legacy, malformed] = parseEnvironmentRuntimes([
    { ...runtime, metadata: { version: "0.118", cli_version: "1.0.24", launched_by: "desktop", server_url: "https://api.example.test" } },
    { ...runtime, id: "legacy" },
    { ...runtime, id: "malformed", metadata: { launched_by: true, cli_version: 24 }, daemon_id: 4 },
  ]);
  expect(current?.metadata).toEqual({ version: "0.118", cli_version: "1.0.24", launched_by: "desktop", server_url: "https://api.example.test" });
  expect(legacy?.metadata).toEqual({});
  expect(malformed?.daemon_id).toBeNull();
  expect(malformed?.metadata.launched_by).toBeUndefined();
  expect(() => parseEnvironmentRuntimes({ runtimes: [] })).toThrow("Invalid environment runtime list");
});

it("shows public API endpoints without credentials, query strings or fragments", () => {
  expect(environmentRuntimeAPIURL("https://user:secret@api.example.test:8443/base/?token=private#secret")).toBe("https://api.example.test:8443/base");
  expect(environmentRuntimeAPIURL("file:///private/secret")).toBeNull();
  expect(environmentRuntimeAPIURL("invalid")).toBeNull();
  expect(environmentRuntimeAPIURL(undefined)).toBeNull();
});
