"use client";

import { useMemo, useState, useCallback, useRef, useEffect } from "react";
import { useDefaultLayout, usePanelRef } from "react-resizable-panels";
import { Check, ChevronRight, Link2, MoreHorizontal, PanelRight, Pin, PinOff, RefreshCw, Trash2, UserMinus } from "lucide-react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { toast } from "sonner";
import type { ProjectCollaborationGraphResponse, ProjectStatus, ProjectPriority } from "@multica/core/types";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { projectDetailOptions } from "@multica/core/projects/queries";
import { projectCollaborationEvidenceInfiniteOptions, projectCollaborationGraphInfiniteOptions } from "@multica/core/collaboration";
import { useUpdateProject, useDeleteProject } from "@multica/core/projects/mutations";
import { pinListOptions } from "@multica/core/pins";
import { useCreatePin, useDeletePin } from "@multica/core/pins";
import { memberListOptions, agentListOptions } from "@multica/core/workspace/queries";
import { squadListOptions } from "@multica/core/workspace/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { useIssuesScope } from "@multica/core/issues/stores";
import { useRecentContextStore } from "@multica/core/chat";
import { useWorkspacePaths } from "@multica/core/paths";
import { useActorName } from "@multica/core/workspace/hooks";
import { PROJECT_STATUS_ORDER, PROJECT_STATUS_CONFIG, PROJECT_PRIORITY_ORDER } from "@multica/core/projects/config";
import { getProjectIssueMetrics } from "./project-issue-metrics";
import { ActorAvatar } from "../../common/actor-avatar";
import { currentPath, useNavigation } from "../../navigation";
import { TitleEditor, ContentEditor, type ContentEditorRef } from "../../editor";
import { PriorityIcon } from "../../issues/components/priority-icon";
import { ProjectResourcesSection } from "./project-resources-section";
import { ProjectStartDatePicker } from "./project-start-date-picker";
import { ProjectDueDatePicker } from "./project-due-date-picker";
import { IssueSurface } from "../../issues/surface/issue-surface";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Button } from "@multica/ui/components/ui/button";
import { ResizablePanelGroup, ResizablePanel, ResizableHandle } from "@multica/ui/components/ui/resizable";
import { Sheet, SheetContent } from "@multica/ui/components/ui/sheet";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@multica/ui/components/ui/popover";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
} from "@multica/ui/components/ui/tooltip";
import { EmojiPicker } from "@multica/ui/components/common/emoji-picker";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import {
  AnimatedRightSidebar,
  getAnimatedRightSidebarInitialOpen,
  rightSidebarPanelMotionProps,
  useAnimatedRightSidebarState,
  useRightSidebarShortcut,
} from "../../layout/animated-right-sidebar";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { useT } from "../../i18n";
import { useProjectStatusLabels, useProjectPriorityLabels } from "./labels";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";
import { CollaborationGraphCanvas } from "../../collaboration/components";

type ProjectDetailTab = "issues" | "graph";

// ---------------------------------------------------------------------------
// Property row — sidebar property display
// ---------------------------------------------------------------------------

function PropRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-h-8 items-center gap-2 rounded-md px-2 -mx-2 hover:bg-accent/50 transition-colors">
      <span className="w-16 shrink-0 text-caption text-muted-foreground">{label}</span>
      <div className="flex min-w-0 flex-1 items-center gap-1.5 text-caption truncate">
        {children}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// ProjectDetail
// ---------------------------------------------------------------------------

export function ProjectDetail({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const { t: collaborationT } = useT("collaboration");
  const statusLabels = useProjectStatusLabels();
  const priorityLabels = useProjectPriorityLabels();
  const wsId = useWorkspaceId();
  const wsPaths = useWorkspacePaths();
  const router = useNavigation();
  const userId = useAuthStore((s) => s.user?.id);
  const { data: project, isLoading } = useQuery(projectDetailOptions(wsId, projectId));
  const [collaborationStatus, setCollaborationStatus] = useState("");
  const collaborationQuery = useInfiniteQuery(projectCollaborationGraphInfiniteOptions(wsId, projectId, collaborationStatus));
  const collaborationGraphPages = collaborationQuery.data?.pages ?? [];
  const collaborationGraph = useMemo<ProjectCollaborationGraphResponse | undefined>(() => {
    const base = collaborationGraphPages[0];
    if (!base) return undefined;
    const nodes = new Map(base.nodes.map((node) => [node.id, node]));
    const edges = new Map(base.edges.map((edge) => [edge.id, edge]));
    for (const page of collaborationGraphPages.slice(1)) {
      for (const node of page.nodes) nodes.set(node.id, node);
      for (const edge of page.edges) edges.set(edge.id, edge);
    }
    return { ...base, nodes: [...nodes.values()], edges: [...edges.values()], has_more: collaborationQuery.hasNextPage ?? false };
  }, [collaborationGraphPages, collaborationQuery.hasNextPage]);
  const recordRecentContext = useRecentContextStore((s) => s.recordVisit);
  useEffect(() => {
    if (project) {
      recordRecentContext(wsId, {
        type: "project",
        id: project.id,
        label: project.title,
        subtitle: project.description ?? undefined,
        icon: project.icon,
        projectStatus: project.status,
      });
    }
  }, [project?.id, project?.title, project?.description, project?.icon, project?.status, recordRecentContext, wsId]);
  const issueTab = useIssuesScope(`project:${projectId}`);
  const issueScope = useMemo(
    () => ({ type: "project" as const, projectId, actorKind: issueTab }),
    [projectId, issueTab],
  );
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const isWorkspaceAdmin = useMemo(() => {
    if (!userId) return false;
    const me = members.find((m) => m.user_id === userId);
    return me?.role === "owner" || me?.role === "admin";
  }, [members, userId]);
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const { data: scopeBindings } = useQuery({
    queryKey: ["project-execution-scope-bindings", wsId, projectId],
    queryFn: () => api.getProjectExecutionScopeBindings(projectId),
    enabled: !!projectId && isWorkspaceAdmin,
  });
  const [scopeAgentIds, setScopeAgentIds] = useState<string[]>([]);
  const [scopeSquadIds, setScopeSquadIds] = useState<string[]>([]);
  const [scopeSaving, setScopeSaving] = useState(false);
  useEffect(() => {
    setScopeAgentIds(scopeBindings?.agent_ids ?? []);
    setScopeSquadIds(scopeBindings?.squad_ids ?? []);
  }, [scopeBindings]);
  const updateScopeBindings = useCallback(async (agentIds: string[], squadIds: string[]) => {
    const previous = { agentIds: scopeAgentIds, squadIds: scopeSquadIds };
    setScopeAgentIds(agentIds);
    setScopeSquadIds(squadIds);
    setScopeSaving(true);
    try {
      await api.updateProjectExecutionScopeBindings(projectId, { agent_ids: agentIds, squad_ids: squadIds });
    } catch (error) {
      setScopeAgentIds(previous.agentIds);
      setScopeSquadIds(previous.squadIds);
      toast.error(error instanceof Error ? error.message : t(($) => $.detail.execution_scope_save_failed));
    } finally {
      setScopeSaving(false);
    }
  }, [projectId, scopeAgentIds, scopeSquadIds]);
  const { getActorName } = useActorName();
  const updateProject = useUpdateProject();
  const deleteProject = useDeleteProject();
  const { data: pinnedItems = [] } = useQuery({
    ...pinListOptions(wsId, userId ?? ""),
    enabled: !!userId,
  });
  const isPinned = pinnedItems.some((p) => p.item_type === "project" && p.item_id === projectId);
  const createPin = useCreatePin();
  const deletePinMut = useDeletePin();
  const descEditorRef = useRef<ContentEditorRef>(null);
  const isMobile = useIsMobile();
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [iconPickerOpen, setIconPickerOpen] = useState(false);
  const [propertiesOpen, setPropertiesOpen] = useState(true);
  const [progressOpen, setProgressOpen] = useState(true);
  const [descriptionOpen, setDescriptionOpen] = useState(true);
  const [activeTab, setActiveTab] = useState<ProjectDetailTab>("issues");
  const [selectedCollaborationEdge, setSelectedCollaborationEdge] = useState<string | undefined>();
  useEffect(() => { setSelectedCollaborationEdge(undefined); }, [wsId, projectId, collaborationStatus]);
  const collaborationEvidenceQuery = useInfiniteQuery(projectCollaborationEvidenceInfiniteOptions(wsId, projectId, selectedCollaborationEdge, collaborationStatus));
  const graphEventsQuery = useQuery({ queryKey: ["project-graph-events", wsId, projectId], queryFn: () => api.listProjectGraphEvents(projectId, { limit: 200 }), enabled: activeTab === "graph" });
  const producedArtifacts = useMemo(() => (graphEventsQuery.data?.events ?? []).flatMap((event) => {
    const artifact = event.data?.artifact;
    const attachmentIDs = Array.isArray(event.data?.artifact_attachment_ids) ? event.data.artifact_attachment_ids.filter((id): id is string => typeof id === "string") : [];
    return artifact && typeof artifact === "object" ? [{ event, artifact: artifact as Record<string, unknown>, attachmentIDs }] : attachmentIDs.length > 0 ? [{ event, artifact: {}, attachmentIDs }] : [];
  }), [graphEventsQuery.data?.events]);
  const collaborationEvidence = useMemo(() => {
    const pages = collaborationEvidenceQuery.data?.pages ?? [];
    return pages.length === 0 ? undefined : { ...pages[0], evidence: pages.flatMap((page) => page.evidence), has_more: collaborationEvidenceQuery.hasNextPage ?? false };
  }, [collaborationEvidenceQuery.data?.pages, collaborationEvidenceQuery.hasNextPage]);

  // Sidebar panel
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: "multica_project_detail_layout",
  });
  const sidebarRef = usePanelRef();
  const rightSidebarShortcutTargetRef = useRef<HTMLDivElement | null>(null);
  const desktopSidebarInitialOpen = getAnimatedRightSidebarInitialOpen(
    true,
    defaultLayout,
  );
  // Desktop and mobile sidebar state must be separate. A single state defaulting
  // to `true` made the mobile <Sheet> mount in the open position on first render
  // (after `useIsMobile()` flipped from false→true), briefly covering the page
  // with its modal backdrop and locking scroll — leaving the page unresponsive.
  const {
    open: desktopSidebarOpen,
    visualOpen: desktopSidebarVisualOpen,
    motionEnabled: desktopSidebarMotionEnabled,
    beginToggle: beginDesktopSidebarToggle,
    handleResize: handleDesktopSidebarResize,
  } = useAnimatedRightSidebarState(desktopSidebarInitialOpen);
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);
  const sidebarOpen = isMobile ? mobileSidebarOpen : desktopSidebarOpen;

  useEffect(() => {
    if (isMobile) {
      setMobileSidebarOpen(false);
    }
  }, [isMobile]);

  const handleToggleSidebar = useCallback(() => {
    if (isMobile) {
      setMobileSidebarOpen((open) => !open);
      return;
    }

    const panel = sidebarRef.current;
    if (!panel) return;
    const nextOpen = panel.isCollapsed();
    beginDesktopSidebarToggle(nextOpen);
    window.requestAnimationFrame(() => {
      if (nextOpen) panel.expand();
      else panel.collapse();
    });
  }, [beginDesktopSidebarToggle, isMobile, sidebarRef]);

  useRightSidebarShortcut(rightSidebarShortcutTargetRef, handleToggleSidebar);

  // Lead popover
  const [leadOpen, setLeadOpen] = useState(false);
  const [leadFilter, setLeadFilter] = useState("");
  const leadQuery = leadFilter.toLowerCase();
  const filteredMembers = members.filter((m) => m.name.toLowerCase().includes(leadQuery) || matchesPinyin(m.name, leadQuery));
  const filteredAgents = agents.filter((a) => !a.archived_at && (a.name.toLowerCase().includes(leadQuery) || matchesPinyin(a.name, leadQuery)));

  const handleUpdateField = useCallback(
    (data: Parameters<typeof updateProject.mutate>[0] extends { id: string } & infer R ? R : never) => {
      if (!project) return;
      updateProject.mutate({ id: project.id, ...data });
    },
    [project, updateProject],
  );

  const handleDelete = useCallback(() => {
    if (!project) return;
    deleteProject.mutate(project.id, {
      onSuccess: () => {
        toast.success(t(($) => $.detail.toast_project_deleted));
        router.push(wsPaths.projects());
      },
    });
  }, [project, deleteProject, router, wsPaths, t]);

  if (isLoading) {
    return (
      <div className="mx-auto w-full max-w-4xl px-8 py-10 space-y-4">
        <Skeleton className="h-5 w-32" />
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-4 w-96" />
        <Skeleton className="h-40 w-full mt-8" />
      </div>
    );
  }

  if (!project) {
    return <div className="flex items-center justify-center h-full text-muted-foreground">{t(($) => $.detail.not_found)}</div>;
  }

  const issueMetrics = getProjectIssueMetrics(project);
  const statusCfg = PROJECT_STATUS_CONFIG[project.status];

  const sidebarContent = (
    <div className="space-y-5">
      {/* Icon + Title */}
      <div>
        <Popover open={iconPickerOpen} onOpenChange={setIconPickerOpen}>
          <PopoverTrigger
            render={
              <button
                type="button"
                className="text-display-sm cursor-pointer rounded-lg p-1 -ml-1 hover:bg-accent/60 transition-colors"
                title={t(($) => $.detail.icon_tooltip)}
              >
                {project.icon || "📁"}
              </button>
            }
          />
          <PopoverContent align="start" className="w-auto p-0">
            <EmojiPicker
              onSelect={(emoji) => {
                handleUpdateField({ icon: emoji });
                setIconPickerOpen(false);
              }}
            />
          </PopoverContent>
        </Popover>
        <TitleEditor
          key={`title-${projectId}`}
          defaultValue={project.title}
          placeholder={t(($) => $.detail.title_placeholder)}
          className="mt-2 w-full text-title-sm font-semibold leading-snug tracking-tight"
          onBlur={(value) => {
            const trimmed = value.trim();
            if (trimmed && trimmed !== project.title) handleUpdateField({ title: trimmed });
          }}
        />
      </div>

      {/* Properties */}
      <div>
        <button
          type="button"
          className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-caption font-medium transition-colors mb-2 hover:bg-accent/70 ${propertiesOpen ? "" : "text-muted-foreground hover:text-foreground"}`}
          onClick={() => setPropertiesOpen(!propertiesOpen)}
        >
          {t(($) => $.detail.section_properties)}
          <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${propertiesOpen ? "rotate-90" : ""}`} />
        </button>
        {propertiesOpen && <div className="space-y-0.5 pl-2">
          <PropRow label={t(($) => $.table.status)}>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <button type="button" className="inline-flex items-center gap-1.5 text-caption hover:text-foreground transition-colors">
                    <span className={cn("size-2 rounded-full", statusCfg.dotColor)} />
                    <span>{statusLabels[project.status]}</span>
                  </button>
                }
              />
              <DropdownMenuContent align="start" className="w-44">
                {PROJECT_STATUS_ORDER.map((s) => (
                  <DropdownMenuItem key={s} onClick={() => handleUpdateField({ status: s as ProjectStatus })}>
                    <span className={cn("size-2 rounded-full", PROJECT_STATUS_CONFIG[s].dotColor)} />
                    <span>{statusLabels[s]}</span>
                    {s === project.status && <Check className="ml-auto h-3.5 w-3.5" />}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </PropRow>
          <PropRow label={t(($) => $.table.priority)}>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <button type="button" className="inline-flex items-center gap-1.5 text-caption hover:text-foreground transition-colors">
                    <PriorityIcon priority={project.priority} />
                    <span>{priorityLabels[project.priority]}</span>
                  </button>
                }
              />
              <DropdownMenuContent align="start" className="w-44">
                {PROJECT_PRIORITY_ORDER.map((p) => (
                  <DropdownMenuItem key={p} onClick={() => handleUpdateField({ priority: p as ProjectPriority })}>
                    <PriorityIcon priority={p} />
                    <span>{priorityLabels[p]}</span>
                    {p === project.priority && <Check className="ml-auto h-3.5 w-3.5" />}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </PropRow>
          <PropRow label={t(($) => $.table.lead)}>
            <Popover open={leadOpen} onOpenChange={(v) => { setLeadOpen(v); if (!v) setLeadFilter(""); }}>
              <PopoverTrigger
                render={
                  <button type="button" className="inline-flex items-center gap-1.5 text-caption hover:text-foreground transition-colors">
                    {project.lead_type && project.lead_id ? (
                      <>
                        <ActorAvatar actorType={project.lead_type} actorId={project.lead_id} size="sm" enableHoverCard showStatusDot />
                        <span className="cursor-pointer">{getActorName(project.lead_type, project.lead_id)}</span>
                      </>
                    ) : (
                      <span className="text-muted-foreground">{t(($) => $.lead.no_lead)}</span>
                    )}
                  </button>
                }
              />
              <PopoverContent align="start" className="w-52 p-0">
                <div className="px-2 py-1.5 border-b">
                  <input
                    type="text"
                    value={leadFilter}
                    onChange={(e) => setLeadFilter(e.target.value)}
                    placeholder={t(($) => $.lead.assign_placeholder)}
                    className="w-full bg-transparent text-body placeholder:text-muted-foreground outline-none"
                  />
                </div>
                <div className="p-1 max-h-60 overflow-y-auto">
                  <button
                    type="button"
                    onClick={() => { handleUpdateField({ lead_type: null, lead_id: null }); setLeadOpen(false); }}
                    className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-body hover:bg-accent transition-colors"
                  >
                    <UserMinus className="h-3.5 w-3.5 text-muted-foreground" />
                    <span className="text-muted-foreground">{t(($) => $.lead.no_lead)}</span>
                  </button>
                  {filteredMembers.length > 0 && (
                    <>
                      <div className="px-2 pt-2 pb-1 text-caption font-medium text-muted-foreground uppercase tracking-wider">{t(($) => $.lead.members_group)}</div>
                      {filteredMembers.map((m) => (
                        <button
                          type="button"
                          key={m.user_id}
                          onClick={() => { handleUpdateField({ lead_type: "member", lead_id: m.user_id }); setLeadOpen(false); }}
                          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-body hover:bg-accent transition-colors"
                        >
                          <ActorAvatar actorType="member" actorId={m.user_id} size="sm" />
                          <span>{m.name}</span>
                        </button>
                      ))}
                    </>
                  )}
                  {filteredAgents.length > 0 && (
                    <>
                      <div className="px-2 pt-2 pb-1 text-caption font-medium text-muted-foreground uppercase tracking-wider">{t(($) => $.lead.agents_group)}</div>
                      {filteredAgents.map((a) => (
                        <button
                          type="button"
                          key={a.id}
                          onClick={() => { handleUpdateField({ lead_type: "agent", lead_id: a.id }); setLeadOpen(false); }}
                          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-body hover:bg-accent transition-colors"
                        >
                          <ActorAvatar actorType="agent" actorId={a.id} size="sm" showStatusDot />
                          <span>{a.name}</span>
                        </button>
                      ))}
                    </>
                  )}
                  {filteredMembers.length === 0 && filteredAgents.length === 0 && leadFilter && (
                    <div className="px-2 py-3 text-center text-body text-muted-foreground">{t(($) => $.lead.no_results)}</div>
                  )}
                </div>
              </PopoverContent>
            </Popover>
          </PropRow>
          <PropRow label={t(($) => $.detail.prop_start_date)}>
            <ProjectStartDatePicker startDate={project.start_date} onUpdate={handleUpdateField} />
          </PropRow>
          <PropRow label={t(($) => $.detail.prop_due_date)}>
            <ProjectDueDatePicker dueDate={project.due_date} onUpdate={handleUpdateField} />
          </PropRow>
          {producedArtifacts.length > 0 && <section className="mt-4 rounded-md border p-3"><h3 className="text-caption font-medium">{t(($) => $.detail.artifact_section)}</h3><div className="mt-2 space-y-2">{producedArtifacts.map(({ event, artifact, attachmentIDs }) => <div key={event.id} className="rounded-md bg-muted/30 p-2 text-[11px]"><div className="font-medium">{t(($) => $.detail.artifact_task, { id: event.task_id.slice(0, 8), event: event.event_type })}</div>{typeof artifact.branch_name === "string" && <div className="text-muted-foreground">{t(($) => $.detail.artifact_branch, { value: artifact.branch_name })}</div>}{typeof artifact.durable_work_dir === "string" && <div className="text-muted-foreground">{t(($) => $.detail.artifact_workdir, { value: artifact.durable_work_dir })}</div>}{typeof artifact.derived_from_task_id === "string" && <div className="text-muted-foreground">{t(($) => $.detail.artifact_derived_from, { value: artifact.derived_from_task_id })}</div>}{attachmentIDs.length > 0 && <div className="text-muted-foreground">{t(($) => $.detail.artifact_attachments, { value: attachmentIDs.join(", ") })}</div>}</div>)}</div></section>}
          {isWorkspaceAdmin && <section className="mt-4 rounded-md border p-3"><h3 className="text-caption font-medium">{t(($) => $.detail.execution_scope)}</h3><p className="mt-1 text-[11px] text-muted-foreground">{t(($) => $.detail.execution_scope_description)}</p><div className="mt-2 space-y-1">{agents.map((agent) => <label key={agent.id} className="flex items-center gap-2 text-caption"><input type="checkbox" disabled={scopeSaving} checked={scopeAgentIds.includes(agent.id)} onChange={(event) => { const next = event.target.checked ? [...scopeAgentIds, agent.id] : scopeAgentIds.filter((id) => id !== agent.id); void updateScopeBindings(next, scopeSquadIds); }} />{agent.name}</label>)}{squads.map((squad) => <label key={squad.id} className="flex items-center gap-2 text-caption"><input type="checkbox" disabled={scopeSaving} checked={scopeSquadIds.includes(squad.id)} onChange={(event) => { const next = event.target.checked ? [...scopeSquadIds, squad.id] : scopeSquadIds.filter((id) => id !== squad.id); void updateScopeBindings(scopeAgentIds, next); }} />{squad.name}</label>)}</div>{scopeSaving && <p className="mt-2 text-[11px] text-muted-foreground">{t(($) => $.detail.execution_scope_saving)}</p>}</section>}
          </div>}
      </div>

      {/* Progress */}
      {issueMetrics.totalCount > 0 && (() => {
        const pct = Math.round((issueMetrics.completedCount / issueMetrics.totalCount) * 100);
        return (
          <div>
            <button
              type="button"
              className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-caption font-medium transition-colors mb-2 hover:bg-accent/70 ${progressOpen ? "" : "text-muted-foreground hover:text-foreground"}`}
              onClick={() => setProgressOpen(!progressOpen)}
            >
              {t(($) => $.detail.section_progress)}
              <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${progressOpen ? "rotate-90" : ""}`} />
            </button>
            {progressOpen && <div className="pl-2 flex items-center gap-3">
              <div className="relative h-2 flex-1 rounded-full bg-muted overflow-hidden">
                <div
                  className="absolute inset-y-0 left-0 rounded-full bg-emerald-500 transition-all"
                  style={{ width: `${pct}%` }}
                />
              </div>
              <span className="text-caption text-muted-foreground tabular-nums shrink-0">
                {issueMetrics.completedCount}/{issueMetrics.totalCount}
              </span>
            </div>}
          </div>
        );
      })()}

      {/* Description */}
      <div>
        <button
          type="button"
          className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-caption font-medium transition-colors mb-2 hover:bg-accent/70 ${descriptionOpen ? "" : "text-muted-foreground hover:text-foreground"}`}
          onClick={() => setDescriptionOpen(!descriptionOpen)}
        >
          {t(($) => $.detail.section_description)}
          <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${descriptionOpen ? "rotate-90" : ""}`} />
        </button>
        {descriptionOpen && <div className="pl-2">
          <ContentEditor
            ref={descEditorRef}
            key={projectId}
            value={project.description || ""}
            placeholder={t(($) => $.detail.description_placeholder)}
            onUpdate={(md) => handleUpdateField({ description: md || null })}
            debounceMs={1500}
          />
          <p className="mt-1 px-2 text-caption text-muted-foreground">
            {t(($) => $.detail.description_hint)}
          </p>
        </div>}
      </div>

      {/* Resources */}
      <ProjectResourcesSection projectId={projectId} />


    </div>
  );

  return (
    <>
    <ResizablePanelGroup orientation="horizontal" className="flex-1 min-h-0" defaultLayout={defaultLayout} onLayoutChanged={onLayoutChanged}>
      <ResizablePanel id="content" minSize="50%">
        <div ref={rightSidebarShortcutTargetRef} className="flex h-full flex-col">
          <BreadcrumbHeader
            segments={[{ href: wsPaths.projects(), label: t(($) => $.detail.breadcrumb_fallback) }]}
            leaf={<span className="truncate font-medium text-foreground">{project.title}</span>}
            actions={
              <>
              <Button
                variant="ghost"
                size="icon-sm"
                className={cn("text-muted-foreground", isPinned && "text-foreground")}
                title={isPinned ? t(($) => $.detail.unpin_tooltip) : t(($) => $.detail.pin_tooltip)}
                onClick={() => {
                  if (isPinned) {
                    deletePinMut.mutate({ itemType: "project", itemId: projectId });
                  } else {
                    createPin.mutate({ item_type: "project", item_id: projectId });
                  }
                }}
              >
                {isPinned ? <PinOff /> : <Pin />}
              </Button>
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button variant="ghost" size="icon-sm" className="text-muted-foreground">
                      <MoreHorizontal />
                    </Button>
                  }
                />
                <DropdownMenuContent align="end" className="w-auto">
                  <DropdownMenuItem onClick={() => {
                    void copyText(router.getShareableUrl(currentPath(router))).then((ok) => {
                      if (ok) toast.success(t(($) => $.detail.toast_link_copied));
                    });
                  }}>
                    <Link2 className="h-3.5 w-3.5" />
                    {t(($) => $.detail.copy_link)}
                  </DropdownMenuItem>
                  {isWorkspaceAdmin && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        variant="destructive"
                        onClick={() => setDeleteDialogOpen(true)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                        {t(($) => $.detail.delete_action)}
                      </DropdownMenuItem>
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      variant={sidebarOpen ? "secondary" : "ghost"}
                      size="icon-sm"
                      className={sidebarOpen ? "" : "text-muted-foreground"}
                      onClick={handleToggleSidebar}
                    >
                      <PanelRight />
                    </Button>
                  }
                />
                <TooltipContent side="bottom">{t(($) => $.detail.sidebar_tooltip)}</TooltipContent>
              </Tooltip>
              </>
            }
          />

          <div className="flex shrink-0 items-center gap-0 overflow-x-auto border-b px-4">
            <button type="button" onClick={() => setActiveTab("issues")} className={cn("border-b-2 px-3 py-2.5 text-caption font-medium", activeTab === "issues" ? "border-foreground text-foreground" : "border-transparent text-muted-foreground hover:text-foreground")}>{collaborationT(($) => $.project_tab_tasks)}</button>
            <button type="button" onClick={() => setActiveTab("graph")} className={cn("border-b-2 px-3 py-2.5 text-caption font-medium", activeTab === "graph" ? "border-foreground text-foreground" : "border-transparent text-muted-foreground hover:text-foreground")}>{collaborationT(($) => $.project_tab_graph)}</button>
          </div>
          {activeTab === "issues" ? <IssueSurface scope={issueScope} modes={["board", "list", "table", "swimlane", "gantt"]} /> : <div className="min-h-0 flex-1 overflow-y-auto p-4 md:p-6"><div className="mb-3 flex items-center justify-between gap-2"><div><h2 className="text-body font-medium">{collaborationT(($) => $.project_title)}</h2><p className="text-caption text-muted-foreground">{collaborationT(($) => $.project_description)}</p></div><div className="flex flex-wrap items-center justify-end gap-2">
<select aria-label={collaborationT(($) => $.status_filter)} className="h-8 rounded-md border bg-background px-2 text-[11px]" value={collaborationStatus} onChange={(event) => setCollaborationStatus(event.target.value)}>
<option value="">{collaborationT(($) => $.status_all)}</option><option value="running">{collaborationT(($) => $.status_running)}</option><option value="completed">{collaborationT(($) => $.status_completed)}</option><option value="failed">{collaborationT(($) => $.status_failed)}</option>
</select>{collaborationGraph ? <span className="text-[11px] text-muted-foreground">{collaborationT(($) => $.agents_count, { count: collaborationGraph.summary.agent_count })}</span> : null}<Button size="sm" variant="ghost" onClick={() => { void collaborationQuery.refetch(); }} disabled={collaborationQuery.isFetching}><RefreshCw className="mr-1 h-3.5 w-3.5" />{collaborationT(($) => $.refresh)}</Button></div></div>{collaborationQuery.isLoading ? <Skeleton className="h-64 w-full" /> : collaborationQuery.isError ? <div className="rounded-md border border-dashed p-6 text-center text-caption text-muted-foreground"><p>{collaborationT(($) => $.load_failed)}</p><Button size="sm" variant="outline" className="mt-3" onClick={() => void collaborationQuery.refetch()}>{collaborationT(($) => $.retry)}</Button></div> : <><CollaborationGraphCanvas nodes={collaborationGraph?.nodes ?? []} edges={collaborationGraph?.edges ?? []} coverage={collaborationGraph?.summary.coverage} coverageReasons={collaborationGraph?.coverage_reasons} asOf={collaborationGraph?.as_of} onEdgeClick={(edge) => setSelectedCollaborationEdge(edge.id)} emptyLabel={collaborationT(($) => $.empty_project)} />{collaborationGraph?.has_more ? <div className="mt-3 flex justify-center"><Button size="sm" variant="outline" onClick={() => collaborationQuery.fetchNextPage()} disabled={collaborationQuery.isFetching}>{collaborationQuery.isFetching ? collaborationT(($) => $.loading_more) : collaborationT(($) => $.load_more)}</Button></div> : null}{selectedCollaborationEdge && <div className="mt-4 rounded-md border p-3"><h3 className="text-caption font-medium">{collaborationT(($) => $.evidence_title)}</h3>{collaborationEvidenceQuery.isLoading ? <p className="mt-2 text-[11px] text-muted-foreground">{collaborationT(($) => $.loading_more)}</p> : collaborationEvidenceQuery.isError ? <div className="mt-2 flex items-center justify-between gap-2 text-[11px] text-destructive"><span>{collaborationT(($) => $.load_failed)}</span><Button size="sm" variant="outline" onClick={() => void collaborationEvidenceQuery.refetch()}>{collaborationT(($) => $.retry)}</Button></div> : collaborationEvidence?.evidence.length ? <><div className="mt-2 space-y-2">{collaborationEvidence.evidence.map((item) => <div key={item.task_id} className="rounded-md bg-muted/30 p-2 text-[11px]"><div className="font-medium">{item.relation_type} · {item.status}</div><div className="text-muted-foreground">{collaborationT(($) => $.task_events, { id: item.task_id, count: item.event_count })}</div></div>)}</div>{collaborationEvidenceQuery.hasNextPage ? <Button size="sm" variant="ghost" className="mt-2" onClick={() => void collaborationEvidenceQuery.fetchNextPage()} disabled={collaborationEvidenceQuery.isFetchingNextPage}>{collaborationEvidenceQuery.isFetchingNextPage ? collaborationT(($) => $.loading_more) : collaborationT(($) => $.load_more)}</Button> : null}</> : <p className="mt-2 text-[11px] text-muted-foreground">{collaborationT(($) => $.no_evidence)}</p>}</div>}</>}</div>}
          </div>
        </ResizablePanel>
        {!isMobile && <ResizableHandle />}
        {!isMobile && (
        <ResizablePanel
          id="sidebar"
          {...rightSidebarPanelMotionProps}
          data-right-sidebar-motion={desktopSidebarMotionEnabled ? "enabled" : undefined}
          defaultSize={desktopSidebarOpen ? 320 : 0}
          minSize={260}
          maxSize={420}
          collapsible
          groupResizeBehavior="preserve-pixel-size"
          panelRef={sidebarRef}
          onResize={handleDesktopSidebarResize}
        >
          <AnimatedRightSidebar open={desktopSidebarVisualOpen} motionEnabled={desktopSidebarMotionEnabled}>
            {sidebarContent}
          </AnimatedRightSidebar>
        </ResizablePanel>
        )}
        {isMobile && (
          <Sheet open={mobileSidebarOpen} onOpenChange={setMobileSidebarOpen}>
            <SheetContent side="right" showCloseButton={false} className="w-[320px] overflow-y-auto p-4">
              {sidebarContent}
            </SheetContent>
          </Sheet>
        )}
      </ResizablePanelGroup>

      {/* Delete confirmation */}
      {isWorkspaceAdmin && (
        <AlertDialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t(($) => $.delete_dialog.title)}</AlertDialogTitle>
              <AlertDialogDescription>
                {t(($) => $.delete_dialog.description)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t(($) => $.delete_dialog.cancel)}</AlertDialogCancel>
              <AlertDialogAction onClick={handleDelete} className="bg-destructive text-white hover:bg-destructive/90">
                {t(($) => $.delete_dialog.confirm)}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
    </>
  );
}
