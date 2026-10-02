// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { Agent, Project, Squad } from "@multica/core/types";
import { resolveChatExecutionScope } from "./execution-scope";

const agent = { id: "agent-1" } as Agent;
const project = (id: string, leadId: string | null = null): Project => ({
  id,
  workspace_id: "ws-1",
  title: id,
  description: null,
  icon: null,
  status: "planned",
  priority: "none",
  lead_type: leadId ? "agent" : null,
  lead_id: leadId,
  start_date: null,
  due_date: null,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  issue_count: 0,
  done_count: 0,
  resource_count: 0,
});
const squad = (id: string, leaderId = "agent-2", memberIds: string[] = []): Squad => ({
  id,
  workspace_id: "ws-1",
  name: id,
  description: "",
  instructions: "",
  avatar_url: null,
  leader_id: leaderId,
  creator_id: "member-1",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  archived_at: null,
  archived_by: null,
  member_preview: memberIds.map((member_id) => ({ member_id, member_type: "agent", role: "member" })),
});

describe("resolveChatExecutionScope", () => {
  it("derives lead and squad projects and selects the only squad by default", () => {
    const projects = [project("p-squad"), project("p-lead", "agent-1"), project("p-other")];
    const squads = [squad("squad-1", "agent-2", ["agent-1"]), squad("squad-2", "agent-3")];
    const scope = resolveChatExecutionScope({
      projects,
      squads: [squads[0]!],
      bindingsByProject: new Map([
        ["p-squad", { agent_ids: [], squad_ids: ["squad-1"] }],
        ["p-lead", { agent_ids: [], squad_ids: [] }],
        ["p-other", { agent_ids: [], squad_ids: [] }],
      ]),
      agent,
      selectedSquadId: null,
    });

    expect(scope.availableSquads.map((item) => item.id)).toEqual(["squad-1"]);
    expect(scope.availableProjects.map((item) => item.id)).toEqual(["p-squad"]);
    expect(scope.defaultSquadId).toBe("squad-1");
    expect(scope.defaultProjectId).toBe("p-squad");
  });

  it("keeps an agent with no narrower relation workspace-scoped", () => {
    const projects = [project("p-1"), project("p-2")];
    const scope = resolveChatExecutionScope({
      projects,
      squads: [],
      bindingsByProject: new Map(projects.map((item) => [item.id, { agent_ids: [], squad_ids: [] }])),
      agent,
      selectedSquadId: null,
    });

    expect(scope.availableProjects).toEqual(projects);
    expect(scope.defaultProjectId).toBeNull();
    expect(scope.defaultSquadId).toBeNull();
  });

  it("applies the selected squad as an additional project scope", () => {
    const projects = [project("p-1"), project("p-2")];
    const scope = resolveChatExecutionScope({
      projects,
      squads: [squad("squad-1", "agent-2", ["agent-1"])],
      bindingsByProject: new Map([
        ["p-1", { agent_ids: ["agent-1"], squad_ids: [] }],
        ["p-2", { agent_ids: [], squad_ids: ["squad-1"] }],
      ]),
      agent,
      selectedSquadId: "squad-1",
    });

    expect(scope.availableProjects.map((item) => item.id)).toEqual(["p-2"]);
  });

  it("does not narrow an agent because another agent has a project binding", () => {
    const projects = [project("p-1"), project("p-2")];
    const scope = resolveChatExecutionScope({
      projects,
      squads: [],
      bindingsByProject: new Map([
        ["p-1", { agent_ids: ["agent-2"], squad_ids: [] }],
        ["p-2", { agent_ids: [], squad_ids: [] }],
      ]),
      agent,
      selectedSquadId: null,
    });

    expect(scope.availableProjects).toEqual(projects);
    expect(scope.workspaceScopeAllowed).toBe(true);
  });
});
