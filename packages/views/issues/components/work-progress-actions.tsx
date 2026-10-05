"use client";

import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueProgressKeys } from "@multica/core/issue-progress";
import { issueKeys } from "@multica/core/issues/queries";
import { useUpdateIssue } from "@multica/core/issues/mutations";
import { useActorName } from "@multica/core/workspace/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import type { ProgressEntry, ProgressAction } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { AppLink } from "../../navigation";
import { AssigneePicker } from "./pickers/assignee-picker";
import { useWorkProgressLabels } from "./work-progress-labels";

export function WorkProgressActions({ entry }: { entry: ProgressEntry }) {
  const { t } = useWorkProgressLabels();
  const workspaceId = useWorkspaceId();
  const client = useQueryClient();
  const paths = useWorkspacePaths();
  const { getActorName } = useActorName();
  const [opened, setOpened] = useState<ProgressAction | null>(null);
  const [text, setText] = useState("");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [receipt, setReceipt] = useState("");
  const key = useRef("");
  const observed = useRef({ revision: entry.revision, runId: entry.run?.id ?? "", missing: entry.next_step?.missing ?? [] });
  const formStale = !!opened && (observed.current.revision !== entry.revision || observed.current.runId !== (entry.run?.id ?? ""));
  const assign = useUpdateIssue();
  const mutation = useMutation({
    mutationFn: ({ action, verdict }: { action: ProgressAction; verdict?: string }) => {
      if (!entry.revision || formStale) throw new Error("Missing issue revision");
      if (action.kind === "member_work") {
        if (!text.trim()) throw new Error("Progress text is required");
        return api.createComment(entry.issue.id, text).then(() => ({ task_id: null, status: "recorded", issue_id: entry.issue.id }));
      }
      if (!key.current) key.current = crypto.randomUUID();
      const supplied = action.kind === "provide_info" && observed.current.missing.length ? observed.current.missing.map(field => `${field}: ${fields[field]?.trim() ?? ""}`).join("\n") : text;
      return api.performProgressAction(entry.issue.id, { kind: action.kind, issue_revision: observed.current.revision!, run_id: observed.current.runId, key: key.current, ...(supplied ? { text: supplied } : {}), ...(verdict ? { verdict } : {}) });
    },
    onSuccess: async result => {
      setReceipt(result.task_id ? t($ => $.work_progress.action_queued) : result.status === "recorded" ? t($ => $.work_progress.action_recorded) : t($ => $.work_progress.action_accepted));
      setOpened(null); setText(""); setFields({}); key.current = "";
      await Promise.all([client.invalidateQueries({ queryKey: issueProgressKeys.all(workspaceId) }), client.invalidateQueries({ queryKey: issueKeys.all(workspaceId) }), client.invalidateQueries({ queryKey: issueKeys.tasks(entry.issue.id) }), client.invalidateQueries({ queryKey: issueKeys.timeline(entry.issue.id) })]);
    },
  });
  const actionLabels: Record<string, string> = {
    continue: t($ => $.work_progress.action_continue), inspect_continue: t($ => $.work_progress.action_inspect_continue), provide_info: t($ => $.work_progress.action_provide_info), review: t($ => $.work_progress.action_review), rerun: t($ => $.work_progress.action_rerun), assign: t($ => $.work_progress.action_assign), resolve_runtime: t($ => $.work_progress.action_runtime), open_blocker: t($ => $.work_progress.action_blocker), member_work: t($ => $.work_progress.action_member_work),
  };
  const assigneeType = entry.assignee_type === "member" || entry.assignee_type === "agent" || entry.assignee_type === "squad" ? entry.assignee_type : null;
  const actions = entry.actions?.filter(action => action.kind !== "respond" && action.kind !== "manual") ?? [];
  const actor = entry.next_step ? { type: entry.next_step.actor_type, id: entry.next_step.actor_id } : actions[0] ? { type: actions[0].actor_type, id: actions[0].actor_id } : null;
  if (!actions.length && !entry.next_step) return null;
  const waitingFact = entry.next_step?.summary ?? (entry.run?.status === "completed" ? t($ => $.work_progress.action_handoff_unknown) : entry.run?.status === "failed" ? t($ => $.work_progress.action_failure_hint) : null);
  return <div className="space-y-2" data-progress-actions>
    {actor && <p className="break-words text-caption">{t($ => $.work_progress.action_actor)}: {actor.id ? getActorName(actor.type, actor.id) : t($ => $.work_progress.action_actor_unknown)}</p>}
    {waitingFact && <p className="break-words text-caption">{waitingFact}</p>}
    {entry.next_step?.missing?.length && opened?.kind !== "provide_info" ? <ul className="list-disc space-y-1 pl-5 text-caption">{entry.next_step.missing.map(field => <li key={field}>{field}</li>)}</ul> : null}
    {entry.next_step?.evidence?.length ? <details><summary className="cursor-pointer text-caption">{t($ => $.work_progress.action_evidence)}</summary><ul className="mt-2 space-y-1 text-caption">{entry.next_step.evidence.map(reference => <li key={reference} className="break-words">{reference}</li>)}</ul></details> : null}
    <div className="flex flex-wrap gap-2">{actions.map((action, index) => {
      const label = actionLabels[action.kind] ?? t($ => $.work_progress.action_scope_hint);
      if (action.kind === "open_blocker" && action.target_issue_id) return <AppLink key={`${action.kind}:${index}`} href={paths.issueDetail(action.target_issue_id)} className="rounded-md border px-3 py-1.5 text-caption hover:bg-accent">{label}</AppLink>;
      if (action.kind === "resolve_runtime") return <AppLink key={action.kind} href={action.actor_type === "agent" && action.actor_id ? paths.agentDetail(action.actor_id) : entry.assignee_type === "agent" && entry.assignee_id ? paths.agentDetail(entry.assignee_id) : paths.runtimes()} className="rounded-md border px-3 py-1.5 text-caption hover:bg-accent">{label}</AppLink>;
      if (action.kind === "assign") return <AssigneePicker key={action.kind} assigneeType={assigneeType} assigneeId={entry.assignee_id} onUpdate={updates => assign.mutate({ id: entry.issue.id, expected_revision: entry.revision, ...updates })} trigger={<Button size="sm" variant="outline">{label}</Button>} />;
      return <div key={action.kind} className="space-y-1"><Button size="sm" variant={index === 0 ? "default" : "outline"} disabled={!action.enabled || mutation.isPending} onClick={() => { observed.current = {revision: entry.revision, runId: entry.run?.id ?? "", missing: entry.next_step?.missing ?? []}; setOpened(action); setReceipt(""); mutation.reset(); setText(""); setFields({}); key.current = ""; }}>{label}</Button>{!action.enabled && <p className="text-micro text-muted-foreground">{action.disabled_reason === "other_member" ? t($ => $.work_progress.action_other_member) : action.disabled_reason === "invocation_not_allowed" ? t($ => $.work_progress.action_permission) : t($ => $.work_progress.action_unavailable)}</p>}</div>;
    })}</div>
    {opened && <div className="space-y-2 rounded-md bg-muted/40 p-3">
      {formStale && <p role="alert" className="text-caption text-destructive">{t($ => $.work_progress.action_changed)}</p>}
      {opened.kind === "rerun" && <p className="text-caption">{t($ => $.work_progress.action_fresh_warning)}</p>}
      {entry.run?.summary && <details open={opened.kind === "review"}><summary className="cursor-pointer text-caption">{t($ => $.work_progress.action_delivery)}</summary><p className="mt-2 whitespace-pre-wrap break-words text-caption">{entry.run.summary}</p></details>}
      {opened.kind === "provide_info" && observed.current.missing.length ? observed.current.missing.map(field => <label key={field} className="block space-y-1 text-caption"><span>{field}</span><Textarea value={fields[field] ?? ""} onChange={event => { setFields(previous => ({ ...previous, [field]: event.target.value })); key.current = ""; }} disabled={mutation.isPending} maxLength={800} /></label>) : <label className="block space-y-1 text-caption"><span>{opened.kind === "provide_info" ? t($ => $.work_progress.action_information) : opened.kind === "review" ? t($ => $.work_progress.action_review_notes) : t($ => $.work_progress.action_optional_notes)}</span><Textarea value={text} onChange={event => { setText(event.target.value); key.current = ""; }} disabled={mutation.isPending} maxLength={8000} /></label>}
      <div className="flex flex-wrap gap-2">{opened.kind === "review" ? <><Button size="sm" disabled={mutation.isPending || formStale} onClick={() => mutation.mutate({ action: opened, verdict: "accept" })}>{t($ => $.work_progress.action_accept)}</Button><Button size="sm" variant="outline" disabled={mutation.isPending || formStale || !text.trim()} onClick={() => mutation.mutate({ action: opened, verdict: "changes" })}>{t($ => $.work_progress.action_changes)}</Button></> : <Button size="sm" disabled={mutation.isPending || formStale || (opened.kind === "provide_info" ? observed.current.missing.some(field => !fields[field]?.trim()) ?? !text.trim() : opened.kind === "member_work" && !text.trim())} aria-busy={mutation.isPending} onClick={() => mutation.mutate({ action: opened })}>{t($ => $.work_progress.action_submit)}</Button>}<Button size="sm" variant="outline" disabled={mutation.isPending} onClick={() => setOpened(null)}>{t($ => $.work_progress.action_cancel)}</Button></div>
      {mutation.isError && <p role="alert" className="text-caption text-destructive">{mutation.error instanceof ApiError && mutation.error.status === 409 ? t($ => $.work_progress.action_changed) : t($ => $.work_progress.action_failed)}</p>}
    </div>}
    {receipt && <p role="status" className="text-caption">{receipt}</p>}
  </div>;
}
