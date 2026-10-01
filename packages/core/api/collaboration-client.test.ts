// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());
const confirmed = { squad_id: "squad", revision: 3, relations: [], members: [] };
const save = () => new ApiClient("https://example.test").updateSquadCollaborationGraph("squad", { expected_revision: 2, relations: [] });

describe("collaboration graph response integrity", () => {
  it.each([
    {}, { ...confirmed, relations: "invalid" }, { ...confirmed, revision: 2 },
    { ...confirmed, squad_id: "other" }, { squad_id: "squad", revision: 3 },
  ])("rejects unconfirmed saves %j", async (body) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    await expect(save()).rejects.toThrow(/squad collaboration/);
  });
  it("accepts the exact next revision", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(confirmed))));
    await expect(save()).resolves.toMatchObject(confirmed);
  });
  it("requests historical revision explicitly", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(confirmed)));
    vi.stubGlobal("fetch", fetch);
    await new ApiClient("https://example.test").getSquadCollaborationGraph("squad", 3);
    expect(fetch.mock.calls[0]?.[0]).toBe("https://example.test/api/squads/squad/collaboration-graph?revision=3");
  });
});
