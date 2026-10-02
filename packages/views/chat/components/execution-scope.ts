import type {
  Agent,
  Project,
  ProjectExecutionScopeBindings,
  Squad,
} from "@multica/core/types";

export interface ChatExecutionScope {
  availableProjects: Project[];
  availableSquads: Squad[];
  defaultProjectId: string | null;
  defaultSquadId: string | null;
  workspaceScopeAllowed: boolean;
}

type ProjectBindingsById = Map<string, ProjectExecutionScopeBindings | undefined>;

/**
 * Derive the effective chat context from the same relationships enforced by
 * the server: project lead, explicit agent binding, and the agent's squad
 * bindings. An empty relationship set means workspace scope; once any
 * narrower relationship exists, unrelated projects and squads stay out of
 * the picker instead of relying on a late server rejection.
 */
export function resolveChatExecutionScope({
  projects,
  squads,
  bindingsByProject,
  agent,
  selectedSquadId,
}: {
  projects: Project[];
  squads: Squad[];
  bindingsByProject: ProjectBindingsById;
  agent: Agent | null;
  selectedSquadId: string | null;
}): ChatExecutionScope {
  const projectList = projects ?? [];
  const squadList = squads ?? [];
  if (!agent) {
    return {
      availableProjects: projectList,
      availableSquads: [],
      defaultProjectId: null,
      defaultSquadId: null,
      workspaceScopeAllowed: true,
    };
  }

  const availableSquads = squadList.filter(
    (squad) =>
      squad.leader_id === agent.id ||
      squad.member_preview?.some(
        (member) => member.member_type === "agent" && member.member_id === agent.id,
      ) === true,
  );
  const availableSquadIds = new Set(availableSquads.map((squad) => squad.id));
  const effectiveSquadId = selectedSquadId && availableSquadIds.has(selectedSquadId)
    ? selectedSquadId
    : availableSquads[0]?.id ?? null;
  const activeSquad = effectiveSquadId
    ? availableSquads.find((squad) => squad.id === effectiveSquadId) ?? null
    : null;

  const agentLeadProjects = new Set(
    projectList
      .filter((project) => project.lead_type === "agent" && project.lead_id === agent.id)
      .map((project) => project.id),
  );
  const explicitlyBoundProjects = new Set<string>();
  const squadBoundProjects = new Set<string>();
  let hasAgentBinding = false;
  let hasSquadBinding = false;

  for (const project of projectList) {
    const bindings = bindingsByProject.get(project.id);
    if (!bindings) continue;
    if (bindings.agent_ids.includes(agent.id)) {
      hasAgentBinding = true;
      explicitlyBoundProjects.add(project.id);
    }
    if (bindings.squad_ids.length > 0) {
      const agentSquadIds = new Set(availableSquads.map((squad) => squad.id));
      if (bindings.squad_ids.some((squadId) => agentSquadIds.has(squadId))) {
        hasSquadBinding = true;
        squadBoundProjects.add(project.id);
      }
    }
  }

  const hasAgentNarrowScope =
    agentLeadProjects.size > 0 || hasAgentBinding || hasSquadBinding;
  const agentAllowed = (projectId: string) =>
    !hasAgentNarrowScope ||
    agentLeadProjects.has(projectId) ||
    explicitlyBoundProjects.has(projectId) ||
    squadBoundProjects.has(projectId);

  let squadAllowed: ((projectId: string) => boolean) | null = null;
  if (activeSquad) {
    const squadLeadProjects = new Set(
      projectList
        .filter(
          (project) =>
            project.lead_type === "agent" && project.lead_id === activeSquad.leader_id,
        )
        .map((project) => project.id),
    );
    const squadBoundProjectIds = new Set<string>();
    let hasNarrowSquadScope = squadLeadProjects.size > 0;
    for (const project of projectList) {
      const bindings = bindingsByProject.get(project.id);
      if (!bindings) continue;
      if (bindings.squad_ids.includes(activeSquad.id)) {
        hasNarrowSquadScope = true;
        squadBoundProjectIds.add(project.id);
      }
    }
    squadAllowed = (projectId) =>
      !hasNarrowSquadScope ||
      squadLeadProjects.has(projectId) ||
      squadBoundProjectIds.has(projectId);
  }

  const availableProjects = projectList.filter(
    (project) => agentAllowed(project.id) && (squadAllowed?.(project.id) ?? true),
  );
  const defaultSquadId =
    activeSquad?.id ?? null;
  const defaultProjectId = hasAgentNarrowScope
    ? (availableProjects[0]?.id ?? null)
    : null;

  return {
    availableProjects,
    availableSquads,
    defaultProjectId,
    defaultSquadId,
    workspaceScopeAllowed: !hasAgentNarrowScope && availableSquads.length === 0,
  };
}
