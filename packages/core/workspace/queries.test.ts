import { describe, expect, it } from "vitest";
import type { Agent, Squad, Workspace } from "../types";
import { agentListOptions, squadListOptions, workspaceBySlugOptions } from "./queries";

function makeWorkspace(slug: string): Workspace {
  return {
    id: `id-${slug}`,
    name: slug,
    slug,
    description: null,
    context: null,
    settings: {},
    repos: [],
    issue_prefix: slug.toUpperCase(),
    avatar_url: null,
    created_at: "",
    updated_at: "",
  };
}

describe("workspaceBySlugOptions", () => {
  const workspaces = [makeWorkspace("acme")];

  it("selects a matching workspace", () => {
    expect(workspaceBySlugOptions("acme").select?.(workspaces)).toEqual(
      workspaces[0],
    );
  });

  it("returns null after an authoritative list omits the slug", () => {
    expect(workspaceBySlugOptions("missing").select?.(workspaces)).toBeNull();
  });
});

describe("agentListOptions", () => {
  it("drops responses that belong to another workspace before caching", () => {
    const options = agentListOptions("ws-1");
    const select = options.select as (agents: Agent[]) => Agent[];
    expect(
      select([
        { id: "a", workspace_id: "ws-1" } as Agent,
        { id: "foreign", workspace_id: "ws-2" } as Agent,
      ]),
    ).toEqual([{ id: "a", workspace_id: "ws-1" }]);
  });

  it("polls only while projected runtime availability can age offline", () => {
    const options = agentListOptions("ws-1");
    const interval = options.refetchInterval;
    expect(typeof interval).toBe("function");
    if (typeof interval !== "function") return;

    const queryState = (data: Agent[]) =>
      interval({ state: { status: "success", data } } as never);

    expect(
      queryState([{ runtime_availability: "online" } as Agent]),
    ).toBe(30_000);
    expect(
      queryState([{ runtime_availability: "unstable" } as Agent]),
    ).toBe(30_000);
    expect(
      queryState([{ runtime_availability: "offline" } as Agent]),
    ).toBe(false);
    expect(
      queryState([{ runtime_availability: undefined } as Agent]),
    ).toBe(false);
    expect(
      queryState([
        {
          archived_at: "2026-09-04T00:00:00Z",
          runtime_availability: "online",
        } as Agent,
      ]),
    ).toBe(false);
    expect(queryState([])).toBe(false);
  });
});

describe("squadListOptions", () => {
  it("drops squads from a different workspace before caching", () => {
    const options = squadListOptions("ws-1");
    const select = options.select as (squads: Squad[]) => Squad[];
    expect(
      select([
        { id: "s1", workspace_id: "ws-1" } as Squad,
        { id: "s2", workspace_id: "ws-2" } as Squad,
      ]),
    ).toEqual([{ id: "s1", workspace_id: "ws-1" }]);
  });
});
