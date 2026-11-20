"use client";

import type { ReactNode } from "react";
import type { Agent, Project, Squad } from "@multica/core/types";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { ApplicationsPage } from "../../applications";
import { MeetingsPage } from "../../meetings";
import { ProjectCollaborationPanel } from "../../collaboration/components/project-collaboration-panel";
import { useT } from "../../i18n";
import { WorkProgressPanel } from "../../issues/components/work-progress-panel";
import { ProjectSupervisionPanel } from "./project-supervision-panel";

export type ProjectDetailTab = "issues" | "attention" | "graph" | "applications" | "meetings";

export function ProjectDetailContent({
  project, canManage, agents, squads, activeTab, onTabChange, children,
}: {
  readonly project: Project;
  readonly canManage: boolean;
  readonly agents: Agent[];
  readonly squads: Squad[];
  readonly activeTab: ProjectDetailTab;
  readonly onTabChange: (tab: ProjectDetailTab) => void;
  readonly children: ReactNode;
}) {
  const { t } = useT("projects");
  const { t: collaborationT } = useT("collaboration");
  const { t: applicationsT } = useT("applications");

  return (
    <Tabs value={activeTab} onValueChange={(value) => {
      if (value === "issues" || value === "attention" || value === "graph" || value === "applications" || value === "meetings") onTabChange(value);
    }} className="min-h-0 flex-1 gap-0 overflow-hidden">
      <div className="shrink-0 overflow-x-auto border-b px-4">
        <TabsList variant="line" className="h-auto justify-start py-1" aria-label={project.title}>
          <TabsTrigger value="issues" className="shrink-0 px-3 py-2 text-caption">{collaborationT(($) => $.project_tab_tasks)}</TabsTrigger>
          <TabsTrigger value="attention" className="shrink-0 px-3 py-2 text-caption">{t(($) => $.work_progress.attention)}</TabsTrigger>
          <TabsTrigger value="graph" className="shrink-0 px-3 py-2 text-caption">{collaborationT(($) => $.project_tab_graph)}</TabsTrigger>
          <TabsTrigger value="applications" className="shrink-0 px-3 py-2 text-caption">{applicationsT(($) => $.title)}</TabsTrigger>
          <TabsTrigger value="meetings" className="shrink-0 px-3 py-2 text-caption">{t(($) => $.detail.meetings)}</TabsTrigger>
        </TabsList>
      </div>
      <TabsContent value="issues" className="flex min-h-0 flex-col overflow-hidden">{children}</TabsContent>
      <TabsContent value="attention" className="flex min-h-0 flex-col overflow-hidden p-4 md:p-6">
        <WorkProgressPanel key={project.id} scope={{ type: "project", id: project.id }} currentProjectId={project.id}
          header={<ProjectSupervisionPanel key={project.id} project={project} canManage={canManage} />} />
      </TabsContent>
      <TabsContent value="graph" className="min-h-0 overflow-y-auto p-4 md:p-6">
        <ProjectCollaborationPanel key={project.id} projectId={project.id} agents={agents} squads={squads} />
      </TabsContent>
      <TabsContent value="applications" className="flex min-h-0 flex-col overflow-hidden">
        <ApplicationsPage key={project.id} projectId={project.id} embedded />
      </TabsContent>
      <TabsContent value="meetings" className="min-h-0 overflow-auto">
        <MeetingsPage key={project.id} initialProjectId={project.id} />
      </TabsContent>
    </Tabs>
  );
}
