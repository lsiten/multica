"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@multica/ui/components/ui/button";
import { useWorkspaceId } from "@multica/core";
import { useWorkspacePaths } from "@multica/core/paths";
import { meetingDetailOptions } from "@multica/core/meetings/queries";
import { useArchiveMeeting, useDownloadMeetingFile, useUpdateMeeting } from "@multica/core/meetings/mutations";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import { runtimeDisplayLabel } from "@multica/core/runtimes";
import type { Meeting, UpdateMeetingRequest } from "@multica/core/types";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";

export function MeetingDetailPage({ meetingId }: { readonly meetingId: string }) {
  const { t } = useT("chat");
  const workspaceId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const detail = useQuery({ ...meetingDetailOptions(workspaceId, meetingId), enabled: workspaceId.length > 0 && meetingId.length > 0 });
  const runtimes = useQuery({ ...runtimeListOptions(workspaceId), enabled: workspaceId.length > 0 });
  const updateMeeting = useUpdateMeeting();
  const archiveMeeting = useArchiveMeeting();
  const downloadMeeting = useDownloadMeetingFile();
  const meeting = detail.data;
  const [editing, setEditing] = useState(false);
  const [documentText, setDocumentText] = useState("");
  const runtimeNames = useMemo(() => new Map((runtimes.data ?? []).map((runtime) => [runtime.id, runtimeDisplayLabel(runtime)])), [runtimes.data]);

  useEffect(() => {
    if (!meeting || editing) return;
    setDocumentText(meetingMarkdownDocument(meeting));
  }, [editing, meeting]);

  if (detail.isLoading) return <main className="flex flex-1 items-center justify-center p-6 text-sm text-muted-foreground">{t(($) => $.input.meeting_finishing)}</main>;
  if (!meeting) return <main className="flex flex-1 items-center justify-center p-6 text-sm text-muted-foreground">{t(($) => $.input.meeting_empty)}</main>;

  const save = async () => {
    const parsed = parseMeetingMarkdown(documentText, meeting);
    const data: UpdateMeetingRequest = parsed;
    await updateMeeting.mutateAsync({ meetingId, data, workspace: workspaceId });
    await detail.refetch();
    setEditing(false);
  };

  const download = async () => {
    const content = await downloadMeeting.mutateAsync({ meetingId, workspace: workspaceId });
    const url = URL.createObjectURL(new Blob([content], { type: "text/markdown;charset=utf-8" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = `${(meeting.title || "meeting-record").replace(/[\\/:*?"<>|]/g, "_").slice(0, 80) || "meeting-record"}.md`;
    link.click();
    URL.revokeObjectURL(url);
  };

  return <main className="min-h-0 flex-1 overflow-auto bg-muted/20 p-4 md:p-8">
    <article className="mx-auto max-w-4xl rounded-xl border border-border/70 bg-background shadow-sm">
      <header className="flex flex-wrap items-start justify-between gap-4 border-b border-border/70 p-6">
        <div className="min-w-0">
          <button type="button" className="mb-3 text-sm text-muted-foreground underline" onClick={() => navigation.push(paths.meetings())}>{t(($) => $.input.meeting_back)}</button>
          <h1 className="text-2xl font-semibold tracking-tight">{meeting.title || t(($) => $.input.meeting_page_title)}</h1>
          <p className="mt-2 text-sm text-muted-foreground">{new Date(meeting.started_at).toLocaleString()} · {t(($) => $.input.meeting_runtime_value, { runtime: meeting.runtime_id ? runtimeNames.get(meeting.runtime_id) ?? meeting.runtime_id : t(($) => $.input.meeting_empty) })}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {editing ? <>
            <Button size="sm" variant="secondary" disabled={updateMeeting.isPending} onClick={() => void save()}>{t(($) => $.input.meeting_save)}</Button>
            <Button size="sm" variant="ghost" disabled={updateMeeting.isPending} onClick={() => setEditing(false)}>{t(($) => $.input.meeting_cancel_edit)}</Button>
          </> : <Button size="sm" variant="secondary" onClick={() => setEditing(true)}>{t(($) => $.input.meeting_edit_document)}</Button>}
          <Button size="sm" variant="ghost" disabled={downloadMeeting.isPending} onClick={() => void download()}>{t(($) => $.input.meeting_download)}</Button>
          <Button size="sm" variant="ghost" disabled={archiveMeeting.isPending} onClick={() => void archiveMeeting.mutateAsync({ meetingId, archived: !meeting.archived_at, workspace: workspaceId }).then(() => detail.refetch())}>{meeting.archived_at ? t(($) => $.input.meeting_unarchive) : t(($) => $.input.meeting_archive)}</Button>
        </div>
      </header>

      {editing ? <div className="p-6 md:p-10"><label className="block text-sm font-medium">{t(($) => $.input.meeting_document)}<textarea className="mt-3 min-h-[36rem] w-full resize-y rounded-lg border border-input bg-background p-4 font-mono text-sm leading-6" aria-label={t(($) => $.input.meeting_document)} value={documentText} onChange={(event) => setDocumentText(event.target.value)} /></label></div> : <div className="space-y-8 p-6 md:p-10">
        <section><h2 className="text-lg font-semibold">{t(($) => $.input.meeting_summary)}</h2><p className="mt-3 whitespace-pre-wrap leading-7 text-muted-foreground">{meeting.summary || t(($) => $.input.meeting_empty)}</p></section>
        <section><h2 className="text-lg font-semibold">{t(($) => $.input.meeting_decisions)}</h2><MeetingList items={meeting.decisions} empty={t(($) => $.input.meeting_empty)} /></section>
        <section><h2 className="text-lg font-semibold">{t(($) => $.input.meeting_action_items)}</h2><MeetingList items={meeting.action_items} empty={t(($) => $.input.meeting_empty)} /></section>
        <section><h2 className="text-lg font-semibold">{t(($) => $.input.meeting_transcript)}</h2><pre className="mt-3 max-h-[32rem] overflow-auto whitespace-pre-wrap break-words rounded-lg bg-muted/40 p-4 font-mono text-sm leading-6 text-muted-foreground">{meeting.transcript || t(($) => $.input.meeting_empty)}</pre></section>
      </div>}
    </article>
  </main>;
}

function meetingMarkdownDocument(meeting: Meeting): string {
  return [
    `# ${meeting.title || "会议记录"}`,
    "",
    `- 主题：${meeting.topic || "未分类"}`,
    meeting.project_id ? `- 项目：${meeting.project_id}` : "- 项目：未关联",
    `- 日期：${meeting.started_at}`,
    "",
    "## 摘要",
    meeting.summary || "",
    "",
    "## 决策",
    ...(meeting.decisions.length ? meeting.decisions.map((item) => `- ${item}`) : ["- 暂无"]),
    "",
    "## 行动项",
    ...(meeting.action_items.length ? meeting.action_items.map((item) => `- ${item}`) : ["- 暂无"]),
    "",
    "## 原始转写",
    meeting.transcript || "",
    "",
  ].join("\n");
}

function parseMeetingMarkdown(documentText: string, meeting: Meeting): UpdateMeetingRequest {
  const lines = documentText.replace(/\r\n/g, "\n").split("\n");
  const title = lines.find((line) => line.startsWith("# "))?.slice(2).trim() || meeting.title;
  const topic = lines.find((line) => line.startsWith("- 主题："))?.slice("- 主题：".length).trim() || meeting.topic;
  const section = (heading: string) => {
    const index = lines.findIndex((line) => line.trim() === heading);
    if (index < 0) return [];
    const end = lines.findIndex((line, lineIndex) => lineIndex > index && line.startsWith("## "));
    return lines.slice(index + 1, end < 0 ? lines.length : end).map((line) => line.trim()).filter(Boolean);
  };
  const summary = section("## 摘要").join("\n");
  const decisions = section("## 决策").map((line) => line.replace(/^[-*]\s*/, "").trim()).filter((line) => line && line !== "暂无");
  const actionItems = section("## 行动项").map((line) => line.replace(/^[-*]\s*/, "").trim()).filter((line) => line && line !== "暂无");
  const transcript = section("## 原始转写").join("\n");
  return { title, topic, summary, transcript, decisions, action_items: actionItems, project_id: meeting.project_id ?? "" };
}

function MeetingList({ items, empty }: { readonly items: readonly string[]; readonly empty: string }) {
  return items.length ? <ul className="mt-3 list-disc space-y-2 pl-5 leading-7 text-muted-foreground">{items.map((item, index) => <li key={`${index}-${item}`}>{item}</li>)}</ul> : <p className="mt-3 text-muted-foreground">{empty}</p>;
}
