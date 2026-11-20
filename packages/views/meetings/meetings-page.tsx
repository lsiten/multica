"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { AudioLines } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { useWorkspaceId } from "@multica/core";
import { useWorkspacePaths } from "@multica/core/paths";
import { defaultStorage } from "@multica/core/platform";
import { meetingListOptions, meetingKeys } from "@multica/core/meetings/queries";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import { runtimeDisplayLabel } from "@multica/core/runtimes";
import { agentListOptions } from "@multica/core/workspace/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import { useArchiveMeeting, useDeleteMeeting, useDownloadMeetingFile, useSendMeetingToAgent, useUpdateMeeting } from "@multica/core/meetings/mutations";
import type { Meeting, UpdateMeetingRequest } from "@multica/core/types";
import { useT } from "../i18n";
import { useNavigation } from "../navigation";
import { useLocalVoice } from "../platform";
import { MeetingRecorder } from "../chat/components/meeting-recorder";

const defaultRuntimeKey = (workspaceId: string) => `meeting-default-runtime:${workspaceId}`;

export function MeetingsPage({ initialProjectId }: { readonly initialProjectId?: string } = {}) {
  const { t } = useT("chat");
  const workspaceId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const queryClient = useQueryClient();
  const { adapter: localVoice } = useLocalVoice();
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [showArchived, setShowArchived] = useState(false);
  const [filterProjectId, setFilterProjectId] = useState(initialProjectId ?? "");
  const [filterAgentId, setFilterAgentId] = useState("");
  const runtimes = useQuery({ ...runtimeListOptions(workspaceId), enabled: workspaceId.length > 0 });
  const meetings = useQuery({ ...meetingListOptions(workspaceId, { search, includeArchived: showArchived, projectId: filterProjectId, agentId: filterAgentId }), enabled: workspaceId.length > 0 });
  const agents = useQuery({ ...agentListOptions(workspaceId), enabled: workspaceId.length > 0 });
  const projects = useQuery({ ...projectListOptions(workspaceId), enabled: workspaceId.length > 0 });
  const [runtimeId, setRuntimeId] = useState(() => defaultStorage.getItem(defaultRuntimeKey(workspaceId)) ?? "");
  const [agentId, setAgentId] = useState("");
  const sendMeeting = useSendMeetingToAgent();
  const downloadMeeting = useDownloadMeetingFile();
  const updateMeeting = useUpdateMeeting();
  const archiveMeeting = useArchiveMeeting();
  const deleteMeeting = useDeleteMeeting();
  const availableRuntimes = useMemo(() => runtimes.data ?? [], [runtimes.data]);
  const selectedRuntime = availableRuntimes.find((runtime) => runtime.id === runtimeId);
  const projectNames = useMemo(() => new Map((projects.data ?? []).map((project) => [project.id, project.title])), [projects.data]);
  const runtimeNames = useMemo(() => new Map(availableRuntimes.map((runtime) => [runtime.id, runtimeDisplayLabel(runtime)])), [availableRuntimes]);
  const projectOptions = useMemo(() => (projects.data ?? []).map((project) => ({ id: project.id, title: project.title })), [projects.data]);

  useEffect(() => {
    const timer = window.setTimeout(() => setSearch(searchInput.trim()), 250);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  useEffect(() => {
    if (initialProjectId) setFilterProjectId(initialProjectId);
  }, [initialProjectId]);

  useEffect(() => {
    if (!runtimeId || availableRuntimes.some((runtime) => runtime.id === runtimeId)) return;
    setRuntimeId("");
  }, [availableRuntimes, runtimeId]);

  useEffect(() => {
    if (runtimeId || !localVoice) return;
    const local = availableRuntimes.find((runtime) => runtime.runtime_mode === "local" && runtime.status === "online");
    if (local) setRuntimeId(local.id);
  }, [availableRuntimes, localVoice, runtimeId]);

  const selectRuntime = (next: string) => {
    setRuntimeId(next);
    if (next) defaultStorage.setItem(defaultRuntimeKey(workspaceId), next);
  };

  const refreshMeetings = () => { void queryClient.invalidateQueries({ queryKey: meetingKeys.all(workspaceId) }); };
  const update = async (meetingId: string, data: UpdateMeetingRequest) => {
    await updateMeeting.mutateAsync({ meetingId, data, workspace: workspaceId });
    refreshMeetings();
  };
  const archive = async (meetingId: string, archived: boolean) => {
    await archiveMeeting.mutateAsync({ meetingId, archived, workspace: workspaceId });
    refreshMeetings();
  };
  const remove = async (meetingId: string) => {
    if (!window.confirm(t(($) => $.input.meeting_delete_confirm))) return;
    await deleteMeeting.mutateAsync({ meetingId, workspace: workspaceId });
    refreshMeetings();
  };

  const download = async (meeting: NonNullable<typeof meetings.data>[number]) => {
    const content = await downloadMeeting.mutateAsync({ meetingId: meeting.id, workspace: workspaceId });
    const blob = new Blob([content], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `${(meeting.title || "meeting-record").replace(/[\\/:*?"<>|]/g, "_").slice(0, 80) || "meeting-record"}.md`;
    link.click();
    URL.revokeObjectURL(url);
  };

  return (
    <main className="flex min-h-0 flex-1 flex-col gap-4 p-4" data-meetings-page>
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <AudioLines aria-hidden="true" />
          <h1 className="text-lg font-semibold">{t(($) => $.input.meeting_page_title)}</h1>
        </div>
        <div className="flex items-center gap-2">
          <label className="text-caption text-muted-foreground" htmlFor="meeting-runtime">
            {t(($) => $.input.meeting_runtime)}
          </label>
          <select
            id="meeting-runtime"
            className="h-9 max-w-64 rounded-md border border-input bg-background px-2 text-sm"
            value={runtimeId}
            onChange={(event) => selectRuntime(event.target.value)}
          >
            <option value="">{t(($) => $.input.meeting_select_runtime)}</option>
            {availableRuntimes.map((runtime) => <option key={runtime.id} value={runtime.id} disabled={runtime.status !== "online"}>{runtimeDisplayLabel(runtime)}</option>)}
          </select>
          <MeetingRecorder
            workspaceId={workspaceId}
            runtimeId={runtimeId || undefined}
            disabled={!runtimeId || meetings.isLoading || selectedRuntime?.status !== "online"}
            onFinished={() => { void queryClient.invalidateQueries({ queryKey: meetingKeys.list(workspaceId) }); }}
            onOpenMeeting={(meetingId) => navigation.push(paths.meetingDetail(meetingId))}
          />
          <label className="sr-only" htmlFor="meeting-search">{t(($) => $.input.meeting_search)}</label>
          <input id="meeting-search" className="h-9 w-48 rounded-md border border-input bg-background px-2 text-sm" value={searchInput} onChange={(event) => setSearchInput(event.target.value)} placeholder={t(($) => $.input.meeting_search)} />
          <label className="sr-only" htmlFor="meeting-project-filter">{t(($) => $.input.meeting_project_filter)}</label>
          <select id="meeting-project-filter" className="h-9 max-w-52 rounded-md border border-input bg-background px-2 text-sm" value={filterProjectId} onChange={(event) => setFilterProjectId(event.target.value)}>
            <option value="">{t(($) => $.input.meeting_all_projects)}</option>
            {projectOptions.map((project) => <option key={project.id} value={project.id}>{project.title}</option>)}
          </select>
          <label className="sr-only" htmlFor="meeting-agent-filter">{t(($) => $.input.meeting_agent_filter)}</label>
          <select id="meeting-agent-filter" className="h-9 max-w-52 rounded-md border border-input bg-background px-2 text-sm" value={filterAgentId} onChange={(event) => setFilterAgentId(event.target.value)}>
            <option value="">{t(($) => $.input.meeting_all_agents)}</option>
            {(agents.data ?? []).map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
          </select>
          <label className="flex items-center gap-1 text-caption text-muted-foreground"><input type="checkbox" checked={showArchived} onChange={(event) => setShowArchived(event.target.checked)} />{t(($) => $.input.meeting_show_archived)}</label>
          <label className="sr-only" htmlFor="meeting-agent">{t(($) => $.input.meeting_agent)}</label>
          <select id="meeting-agent" className="h-9 max-w-56 rounded-md border border-input bg-background px-2 text-sm" value={agentId} onChange={(event) => setAgentId(event.target.value)}>
            <option value="">{t(($) => $.input.meeting_agent)}</option>
            {(agents.data ?? []).filter((agent) => !agent.archived_at).map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
          </select>
        </div>
      </header>
      {!runtimeId && <p className="text-sm text-muted-foreground">{t(($) => $.input.meeting_select_runtime)}</p>}
      <section className="min-h-0 flex-1 overflow-auto rounded-md border border-border/60">
        {meetings.isLoading ? <p className="p-4 text-sm text-muted-foreground">{t(($) => $.input.meeting_finishing)}</p>
          : meetings.data?.length ? <ul className="divide-y divide-border/60">{meetings.data.map((meeting) => <MeetingRow key={meeting.id} meeting={meeting} runtimeLabel={meeting.runtime_id ? runtimeNames.get(meeting.runtime_id) ?? meeting.runtime_id : t(($) => $.input.meeting_empty)} projectNames={projectNames} projectOptions={projectOptions} agentId={agentId} busy={updateMeeting.isPending || archiveMeeting.isPending || deleteMeeting.isPending} downloadBusy={downloadMeeting.isPending} sendBusy={sendMeeting.isPending} onDownload={download} onSend={(id) => { const target = meetings.data?.find((item) => item.id === id); if (target) void sendMeeting.mutateAsync({ meeting: target, agentId }); }} onUpdate={update} onArchive={archive} onDelete={remove} onDetails={(id) => navigation.push(paths.meetingDetail(id))} />)}</ul>
          : <p className="p-4 text-sm text-muted-foreground">{t(($) => $.input.meeting_empty)}</p>}
      </section>
    </main>
  );
}

function MeetingRow({
  meeting,
  runtimeLabel,
  projectNames,
  projectOptions,
  agentId,
  busy,
  downloadBusy,
  sendBusy,
  onDownload,
  onSend,
  onUpdate,
  onArchive,
  onDelete,
  onDetails,
}: {
  readonly meeting: Meeting;
  readonly runtimeLabel: string;
  readonly projectNames: ReadonlyMap<string, string>;
  readonly projectOptions: readonly { id: string; title: string }[];
  readonly agentId: string;
  readonly busy: boolean;
  readonly downloadBusy: boolean;
  readonly sendBusy: boolean;
  readonly onDownload: (meeting: Meeting) => Promise<void>;
  readonly onSend: (meetingId: string) => void;
  readonly onUpdate: (meetingId: string, data: UpdateMeetingRequest) => Promise<void>;
  readonly onArchive: (meetingId: string, archived: boolean) => Promise<void>;
  readonly onDelete: (meetingId: string) => Promise<void>;
  readonly onDetails: (meetingId: string) => void;
}) {
  const { t } = useT("chat");
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(meeting.title);
  const [topic, setTopic] = useState(meeting.topic);
  const [summary, setSummary] = useState(meeting.summary ?? "");
  const [projectId, setProjectId] = useState(meeting.project_id ?? "");

  useEffect(() => {
    if (editing) return;
    setTitle(meeting.title);
    setTopic(meeting.topic);
    setSummary(meeting.summary ?? "");
    setProjectId(meeting.project_id ?? "");
  }, [editing, meeting.title, meeting.topic, meeting.summary, meeting.project_id]);

  const save = async () => {
    await onUpdate(meeting.id, { title, topic, summary, project_id: projectId });
    setEditing(false);
  };

  return (
    <li className="flex items-start justify-between gap-3 p-4">
      <div className="min-w-0 flex-1">
        {editing ? <div className="flex flex-col gap-2">
          <input className="h-9 rounded-md border border-input bg-background px-2 text-sm" aria-label={t(($) => $.input.meeting_title)} value={title} onChange={(event) => setTitle(event.target.value)} />
          <input className="h-9 rounded-md border border-input bg-background px-2 text-sm" aria-label={t(($) => $.input.meeting_topic)} value={topic} onChange={(event) => setTopic(event.target.value)} />
          <select className="h-9 rounded-md border border-input bg-background px-2 text-sm" aria-label={t(($) => $.input.meeting_project)} value={projectId} onChange={(event) => setProjectId(event.target.value)}>
            <option value="">{t(($) => $.input.meeting_project_none)}</option>
            {projectOptions.map((project) => <option key={project.id} value={project.id}>{project.title}</option>)}
          </select>
          <textarea className="min-h-20 rounded-md border border-input bg-background p-2 text-sm" aria-label={t(($) => $.input.meeting_summary)} value={summary} onChange={(event) => setSummary(event.target.value)} />
        </div> : <>
          <div className="font-medium">{meeting.title || t(($) => $.input.meeting_page_title)}</div>
          <div className="text-caption text-muted-foreground">{meeting.topic || t(($) => $.input.meeting_empty)} · {new Date(meeting.started_at).toLocaleString()}</div>
          <div className="text-caption text-muted-foreground">{t(($) => $.input.meeting_runtime_value, { runtime: runtimeLabel })}</div>
          {meeting.project_id && <div className="text-caption text-muted-foreground">{t(($) => $.input.meeting_project_value, { project: projectNames.get(meeting.project_id) ?? meeting.project_id })}</div>}
          {meeting.summary && <p className="line-clamp-3 text-sm text-muted-foreground">{meeting.summary}</p>}
        </>}
        {!editing && <Button size="sm" variant="ghost" className="mt-2 px-0" onClick={() => onDetails(meeting.id)}>{t(($) => $.input.meeting_details)}</Button>}
      </div>
      <div className="flex shrink-0 flex-wrap items-center justify-end gap-2">
        {editing ? <>
          <Button size="sm" variant="secondary" disabled={busy} onClick={() => void save()}>{t(($) => $.input.meeting_save)}</Button>
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => setEditing(false)}>{t(($) => $.input.meeting_cancel_edit)}</Button>
        </> : <>
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => setEditing(true)}>{t(($) => $.input.meeting_edit)}</Button>
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => void onArchive(meeting.id, !meeting.archived_at)}>{meeting.archived_at ? t(($) => $.input.meeting_unarchive) : t(($) => $.input.meeting_archive)}</Button>
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => void onDelete(meeting.id)}>{t(($) => $.input.meeting_delete)}</Button>
          <Button size="sm" variant="ghost" disabled={downloadBusy} onClick={() => void onDownload(meeting)}>{t(($) => $.input.meeting_download)}</Button>
          <Button size="sm" variant="secondary" disabled={!agentId || sendBusy} onClick={() => onSend(meeting.id)}>{t(($) => $.input.meeting_send)}</Button>
        </>}
      </div>
    </li>
  );
}
