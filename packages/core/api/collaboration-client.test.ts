// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());
const confirmed = { squad_id: "squad", revision: 3, relations: [], members: [] };
const save = () => new ApiClient("https://example.test").updateSquadCollaborationGraph("squad", { expected_revision: 2, relations: [] });

describe("collaboration graph response integrity", () => {
  it("requests a complete graph and preserves cursor and node intersection fields", async () => {
    const fetch = vi.fn().mockImplementation(async()=>new Response(JSON.stringify({project_id:"project",evidence:[],next_cursor:"next",as_of:"snapshot",has_more:true})));
    vi.stubGlobal("fetch",fetch);
    const client = new ApiClient("https://example.test");
    const evidence = await client.getProjectCollaborationEvidence("project",{agent_id:"a",node_agent_id:"b",sort:"oldest",cursor:"cursor",snapshot_at:"snapshot",cursor_mode:true});
    const evidenceUrl = new URL(String(fetch.mock.calls[0]?.[0]));
    expect(Object.fromEntries(evidenceUrl.searchParams)).toMatchObject({agent_id:"a",node_agent_id:"b",sort:"oldest",cursor:"cursor",snapshot_at:"snapshot",cursor_mode:"true"});
    expect(evidence.next_cursor).toBe("next");
    await client.getProjectCollaborationGraph("project",{complete:true});
    expect(String(fetch.mock.calls[1]?.[0])).toContain("complete=true");
  });
  it("defaults optional cursor fields for installed-client response compatibility",async()=>{
    vi.stubGlobal("fetch",vi.fn().mockResolvedValue(new Response(JSON.stringify({project_id:"project",evidence:[]}))));
    expect((await new ApiClient("https://example.test").getProjectCollaborationEvidence("project")).next_cursor).toBeNull();
  });
  it("rejects a malformed evidence cursor through the response schema",async()=>{
    vi.stubGlobal("fetch",vi.fn().mockResolvedValue(new Response(JSON.stringify({project_id:"project",evidence:[],next_cursor:42}))));
    expect((await new ApiClient("https://example.test").getProjectCollaborationEvidence("project")).project_id).toBe("");
  });
  it.each([{}, {agent_ids:"invalid",squad_ids:[]}, null])("rejects malformed scope reads instead of loading an empty writable scope %j", async body => {
    vi.stubGlobal("fetch",vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    await expect(new ApiClient("https://example.test").getProjectExecutionScopeBindings("project")).rejects.toThrow(/scope/);
  });
  it.each([{}, {agent_ids:[],squad_ids:[]}, {agent_ids:["other"],squad_ids:[]}])("rejects an unconfirmed scope save %j", async body => {
    vi.stubGlobal("fetch",vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    await expect(new ApiClient("https://example.test").updateProjectExecutionScopeBindings("project",{agent_ids:["requested"],squad_ids:[]})).rejects.toThrow(/scope/);
  });
  it("serializes task-state and relation filters for the aggregate project view", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ project_id: "project", nodes: [], edges: [], summary: {}, as_of: "" })));
    vi.stubGlobal("fetch", fetch);
    await new ApiClient("https://example.test").getProjectCollaborationGraph("project", { activity: "active", relation_type: "delegated", agent_id: "agent" });
    expect(fetch.mock.calls[0]?.[0]).toBe("https://example.test/api/projects/project/collaboration-graph?agent_id=agent&activity=active&relation_type=delegated");
  });
  it("preserves derived project execution scope sources", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      agent_ids: ["explicit"], squad_ids: ["squad"], auto_agent_ids: ["lead"], inherited_agent_ids: ["worker"], effective_agent_ids: ["lead", "explicit", "worker"],
    }))));
    await expect(new ApiClient("https://example.test").getProjectExecutionScopeBindings("project")).resolves.toMatchObject({ auto_agent_ids: ["lead"], effective_agent_ids: ["lead", "explicit", "worker"] });
  });

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
  it.each([{}, { ...confirmed, squad_id: "other" }, { ...confirmed, members: "invalid" }])("surfaces invalid graph reads instead of showing disabled empty controls %j", async (body) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    await expect(new ApiClient("https://example.test").getSquadCollaborationGraph("squad")).rejects.toThrow("Invalid squad collaboration graph response");
  });
  it("accepts null relation arrays in an older empty graph", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...confirmed, relations: null, derived_relations: null }))));
    await expect(new ApiClient("https://example.test").getSquadCollaborationGraph("squad")).resolves.toMatchObject({ relations: [], derived_relations: [] });
  });
  it("requests historical revision explicitly", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(confirmed)));
    vi.stubGlobal("fetch", fetch);
    await new ApiClient("https://example.test").getSquadCollaborationGraph("squad", 3);
    expect(fetch.mock.calls[0]?.[0]).toBe("https://example.test/api/squads/squad/collaboration-graph?revision=3");
  });
  it("keeps the graph editable when older responses encode deliverables as null", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      squad_id: "squad",
      revision: 0,
      members: [
        { member_id: "leader", member_type: "agent", label: "Leader", role: "" },
        { member_id: "worker", member_type: "agent", label: "Worker", role: "" },
      ],
      relations: [],
      derived_relations: [{
        id: "derived",
        from_member_id: "leader",
        to_member_id: "worker",
        from_member_type: "agent",
        to_member_type: "agent",
        type: "coordinate",
        label: "Squad leader coordination",
        trigger: null,
        deliverables: null,
        acceptance: null,
      }],
    }))));

    const graph = await new ApiClient("https://example.test").getSquadCollaborationGraph("squad");
    expect(graph.members).toHaveLength(2);
    expect(graph.derived_relations?.[0]?.deliverables).toEqual([]);
    expect(graph.derived_relations?.[0]?.trigger).toBe("");
  });
});
