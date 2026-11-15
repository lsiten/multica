// @vitest-environment node
import { describe, expect, it } from "vitest";
import { envelope, scope, sourceWire } from "./vscreen-fixtures";
import { parseVscreenSources } from "./vscreen-state";

describe("mirror source catalog", () => {
  it.each([
    {
      ...sourceWire,
      resource: {
        ...sourceWire.resource,
        backend_identity: "https://foreign.test",
      },
    },
    {
      ...sourceWire,
      resource: { ...sourceWire.resource, runtime_id: "foreign" },
    },
    {
      ...sourceWire,
      resource: { ...sourceWire.resource, workspace_id: "foreign" },
    },
    { ...sourceWire, source: { ...sourceWire.source, kind: "virtual" } },
    { ...sourceWire, generation: "" },
    { ...sourceWire, width: -1 },
  ])("rejects an unbound or input-capable source: %j", (source) => {
    expect(
      parseVscreenSources({ ...envelope, sources: [source] }, scope),
    ).toBeNull();
  });

  it("rejects duplicate source identities", () => {
    expect(
      parseVscreenSources(
        { ...envelope, sources: [sourceWire, sourceWire] },
        scope,
      ),
    ).toBeNull();
  });

  it("retains distinct physical and system sources for read-only viewing", () => {
    const physical = {
      ...sourceWire,
      source: { kind: "physical", source_id: "display:1" },
      primary: true,
    };
    const system = {
      ...sourceWire,
      source: { kind: "system", source_id: "display:2" },
      primary: false,
    };
    const result = parseVscreenSources(
      { ...envelope, sources: [physical, system] },
      scope,
    );
    expect(result?.sources.map((source) => source.source.kind)).toEqual([
      "physical",
      "system",
    ]);
  });
});
