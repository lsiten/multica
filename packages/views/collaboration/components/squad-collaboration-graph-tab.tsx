"use client";
import { useEffect, useId, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@multica/core/api";
import { collaborationGraphKeys, squadCollaborationHistoryOptions } from "@multica/core/collaboration";
import { useWorkspaceId } from "@multica/core/hooks";
import type { SquadCollaborationGraphResponse, SquadCollaborationRelation } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { CollaborationGraphCanvas } from "./collaboration-graph";

export function SquadCollaborationGraphTab({
  squadId,
  graph,
  canManage,
  onDirtyChange,
}: {
  squadId: string;
  graph?: SquadCollaborationGraphResponse;
  canManage: boolean;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const { t: collaborationT } = useT("collaboration");
  const queryClient = useQueryClient();
  const wsId = useWorkspaceId();
  const editorHintId = useId();
  const relationRefs = useRef(new Map<string, HTMLElement>());
  const [relations, setRelations] = useState<SquadCollaborationRelation[]>([]);
  const [dirty, setDirty] = useState(false);
  const [baseRevision, setBaseRevision] = useState(graph?.revision ?? 0);
  const [historyRevision, setHistoryRevision] = useState<number | null>(null);
  const history = useQuery(squadCollaborationHistoryOptions(wsId, squadId, historyRevision));
  useEffect(() => {
    if (!dirty) {
      setRelations(graph?.relations ?? []);
      setBaseRevision(graph?.revision ?? 0);
    }
  }, [dirty, graph?.revision, graph?.relations]);
  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);
  const save = useMutation({
    mutationFn: () => api.updateSquadCollaborationGraph(squadId, {
      expected_revision: baseRevision,
      relations: relations.map(({ id: _id, ...relation }) => ({ ...relation, deliverables: relation.deliverables.map((item) => item.trim()).filter(Boolean) })),
    }),
    onSuccess: (next) => {
      setRelations(next.relations);
      setBaseRevision(next.revision);
      setDirty(false);
      queryClient.setQueryData(collaborationGraphKeys.squad(wsId, squadId), next);
      queryClient.invalidateQueries({ queryKey: ["collaboration-graph", "squad", wsId, squadId] });
    },
  });
  const members = graph?.members ?? [];
  const addRelation = () => {
    if (members.length < 2) return;
    const pair = members.flatMap((from) => members.map((to) => ({ from, to }))).find(({ from, to }) =>
      (from.member_id !== to.member_id || from.member_type !== to.member_type) &&
      !relations.some((relation) => relation.type === "handoff" &&
        relation.from_member_id === from.member_id && relation.from_member_type === from.member_type &&
        relation.to_member_id === to.member_id && relation.to_member_type === to.member_type));
    const from = pair?.from ?? members[0];
    const to = pair?.to ?? members[1];
    if (!from || !to) return;
    setRelations((current) => [...current, {
      id: `draft-${crypto.randomUUID()}`,
      from_member_id: from.member_id, to_member_id: to.member_id,
      from_member_type: from.member_type, to_member_type: to.member_type,
      type: "handoff", label: "", trigger: "", deliverables: [], acceptance: "",
    }]);
    setDirty(true);
  };
  const updateRelation = (id: string, patch: Partial<SquadCollaborationRelation>) => { setRelations((current) => current.map((item) => item.id === id ? { ...item, ...patch } : item)); setDirty(true); };
  const removeRelation = (id: string) => { setRelations((current) => current.filter((item) => item.id !== id)); setDirty(true); };
  const displayedGraph = historyRevision === null ? graph : history.data;
  const displayedRelations = historyRevision === null ? relations : displayedGraph?.relations ?? [];
  const displayedMembers = displayedGraph?.members ?? [];
  const derivedRelations = displayedGraph?.derived_relations ?? [];
  const relationLabels: Record<string, string> = {
    coordinate: collaborationT(($) => $.coordinate),
    handoff: collaborationT(($) => $.handoff),
    review: collaborationT(($) => $.review),
    accept: collaborationT(($) => $.accept),
  };
  const changedRemotely = dirty && graph?.revision !== baseRevision;
  const isHistorical = historyRevision !== null;
  const canEdit = canManage && !isHistorical;
  const saveConflict = changedRemotely || (save.error instanceof ApiError && save.error.status === 409);
  const relationKeys = relations.map((relation) => `${relation.from_member_type}:${relation.from_member_id}:${relation.to_member_type}:${relation.to_member_id}:${relation.type}`);
  const invalidRelations = relations.some((relation) =>
    (relation.from_member_id === relation.to_member_id && relation.from_member_type === relation.to_member_type) ||
    !members.some((member) => member.member_id === relation.from_member_id && member.member_type === relation.from_member_type) ||
    !members.some((member) => member.member_id === relation.to_member_id && member.member_type === relation.to_member_type));
  const duplicateRelations = new Set(relationKeys).size !== relationKeys.length;
  const editorHint = !canManage
    ? collaborationT(($) => $.read_only)
    : isHistorical
      ? collaborationT(($) => $.history_read_only)
      : members.length < 2
        ? collaborationT(($) => $.need_members)
        : relations.length === 0
          ? collaborationT(($) => $.empty_relation_hint)
          : null;
  return (
    <div className="space-y-3">
      <label className="flex items-center gap-2 text-caption">
        {collaborationT(($) => $.history)}
        <input type="number" min={1} max={graph?.revision ?? 0} value={historyRevision ?? ""} placeholder={collaborationT(($) => $.current_revision)} aria-label={collaborationT(($) => $.history)} className="h-8 w-28 rounded-md border bg-background px-2" onChange={(event) => { const revision = Number(event.target.value); setHistoryRevision(Number.isInteger(revision) && revision > 0 ? revision : null); }} />
        <span>{collaborationT(($) => $.revision, { revision: graph?.revision ?? 0 })}</span>
        {historyRevision !== null && <Button size="sm" variant="outline" onClick={() => setHistoryRevision(null)}>{collaborationT(($) => $.current_revision)}</Button>}
      </label>
      {historyRevision !== null && history.isFetching && <p role="status">{collaborationT(($) => $.loading_more)}</p>}
      {historyRevision !== null && history.isError && <p role="alert" className="text-destructive">{collaborationT(($) => $.load_failed)}</p>}
      {editorHint && <p id={editorHintId} role="status" className="rounded-md bg-muted/40 px-3 py-2 text-caption text-muted-foreground">{editorHint}</p>}
      <CollaborationGraphCanvas
        showEvidence={false}
        ariaLabel={collaborationT(($) => $.squad_title)}
        footer={collaborationT(($) => $.agreement_footer)}
        onEdgeClick={(edge) => {
          const editor = relationRefs.current.get(edge.id);
          editor?.scrollIntoView({ block: "nearest", behavior: "smooth" });
          (editor?.querySelector<HTMLInputElement>("input") ?? editor)?.focus({ preventScroll: true });
        }}
        nodes={displayedMembers.map((member) => ({ id: `${member.member_type}:${member.member_id}`, type: "agent" as const, label: member.label, data: { role: member.role } }))}
        edges={[
          ...derivedRelations.map((relation) => ({ id: `derived:${relation.id}`, from: `${relation.from_member_type}:${relation.from_member_id}`, to: `${relation.to_member_type}:${relation.to_member_id}`, type: relation.type, count: 1, active_count: 0, evidence_count: 0, last_event_at: null })),
          ...displayedRelations.map((relation) => ({ id: relation.id, from: `${relation.from_member_type}:${relation.from_member_id}`, to: `${relation.to_member_type}:${relation.to_member_id}`, type: relation.type, count: 1, active_count: 0, evidence_count: 0, last_event_at: null })),
        ]}
        emptyLabel={collaborationT(($) => $.empty_squad)}
      />
      {derivedRelations.length > 0 && <p className="text-caption text-muted-foreground">{collaborationT(($) => $.leader_relations)}</p>}
      {(!canManage || historyRevision !== null) && displayedRelations.map((relation) => (
        <article key={relation.id} tabIndex={-1} ref={(node) => { if (node) relationRefs.current.set(relation.id, node); else relationRefs.current.delete(relation.id); }} className="space-y-1 rounded-md border p-3 text-caption focus-visible:outline-ring">
          <h4 className="font-medium">{relation.label || relationLabels[relation.type] || relation.type}</h4>
          <p className="break-words text-muted-foreground">{displayedMembers.find((member) => member.member_id === relation.from_member_id && member.member_type === relation.from_member_type)?.label} → {displayedMembers.find((member) => member.member_id === relation.to_member_id && member.member_type === relation.to_member_type)?.label} · {relationLabels[relation.type] || relation.type}</p>
          {relation.trigger && <p>{relation.trigger}</p>}
          {relation.deliverables.length > 0 && <ul className="list-disc pl-4">{relation.deliverables.map((item, index) => <li key={index}>{item}</li>)}</ul>}
          {relation.acceptance && <p>{relation.acceptance}</p>}
        </article>
      ))}
      {canEdit && (
        <fieldset disabled={save.isPending} className="space-y-2 rounded-md border p-3">
          <div className="flex items-center justify-between gap-2">
            <span className="text-caption font-medium">{collaborationT(($) => $.relations_title)}</span>
            <Button
              size="sm"
              variant="outline"
              onClick={addRelation}
              disabled={save.isPending || members.length < 2}
              title={members.length < 2 ? collaborationT(($) => $.need_members) : undefined}
              aria-describedby={editorHint ? editorHintId : undefined}
            >
              {collaborationT(($) => $.add_relation)}
            </Button>
          </div>
          {relations.map((relation) => (
            <div key={relation.id} ref={(node) => { if (node) relationRefs.current.set(relation.id, node); else relationRefs.current.delete(relation.id); }} className="space-y-2 rounded-md bg-muted/30 p-2">
              <div className="grid grid-cols-2 gap-2">
                <select aria-label={collaborationT(($) => $.from_member)} className="h-8 rounded-md border bg-background px-2 text-caption" value={`${relation.from_member_type}:${relation.from_member_id}`} onChange={(event) => { const [type, id] = event.target.value.split(":"); updateRelation(relation.id, { from_member_type: type as "agent" | "member", from_member_id: id }); }}>
                  {members.map((member) => <option key={`${member.member_type}:${member.member_id}`} value={`${member.member_type}:${member.member_id}`} disabled={member.member_id === relation.to_member_id && member.member_type === relation.to_member_type}>{member.label}</option>)}
                </select>
                <select aria-label={collaborationT(($) => $.to_member)} className="h-8 rounded-md border bg-background px-2 text-caption" value={`${relation.to_member_type}:${relation.to_member_id}`} onChange={(event) => { const [type, id] = event.target.value.split(":"); updateRelation(relation.id, { to_member_type: type as "agent" | "member", to_member_id: id }); }}>
                  {members.map((member) => <option key={`${member.member_type}:${member.member_id}`} value={`${member.member_type}:${member.member_id}`} disabled={member.member_id === relation.from_member_id && member.member_type === relation.from_member_type}>{member.label}</option>)}
                </select>
              </div>
              <div className="grid grid-cols-2 gap-2">
                <select aria-label={collaborationT(($) => $.relation_type)} className="h-8 rounded-md border bg-background px-2 text-caption" value={relation.type} onChange={(event) => updateRelation(relation.id, { type: event.target.value as SquadCollaborationRelation["type"] })}>
                  <option value="coordinate">{collaborationT(($) => $.coordinate)}</option><option value="handoff">{collaborationT(($) => $.handoff)}</option><option value="review">{collaborationT(($) => $.review)}</option><option value="accept">{collaborationT(($) => $.accept)}</option>
                </select>
                <input aria-label={collaborationT(($) => $.relation_label)} className="h-8 min-w-0 rounded-md border bg-background px-2 text-caption" value={relation.label} placeholder={collaborationT(($) => $.relation_label)} onChange={(event) => updateRelation(relation.id, { label: event.target.value })} />
              </div>
              <input aria-label={collaborationT(($) => $.trigger)} className="h-8 w-full rounded-md border bg-background px-2 text-caption" value={relation.trigger} placeholder={collaborationT(($) => $.trigger)} onChange={(event) => updateRelation(relation.id, { trigger: event.target.value })} />
              <textarea aria-label={collaborationT(($) => $.deliverables)} className="min-h-14 w-full rounded-md border bg-background px-2 py-1 text-caption" value={relation.deliverables.join("\n")} placeholder={collaborationT(($) => $.deliverables)} onChange={(event) => updateRelation(relation.id, { deliverables: event.target.value.split("\n") })} />
              <textarea aria-label={collaborationT(($) => $.acceptance)} className="min-h-14 w-full rounded-md border bg-background px-2 py-1 text-caption" value={relation.acceptance} placeholder={collaborationT(($) => $.acceptance)} onChange={(event) => updateRelation(relation.id, { acceptance: event.target.value })} />
              <Button size="sm" variant="ghost" className="text-destructive" onClick={() => removeRelation(relation.id)}>{collaborationT(($) => $.delete)}</Button>
            </div>
          ))}
          <Button
            size="sm"
            onClick={() => save.mutate()}
            disabled={save.isPending || !dirty || !graph || invalidRelations || duplicateRelations}
            aria-busy={save.isPending}
            title={!dirty ? collaborationT(($) => $.no_changes) : undefined}
            aria-describedby={editorHint ? editorHintId : undefined}
          >
            {save.isPending ? collaborationT(($) => $.saving) : collaborationT(($) => $.save)}
          </Button>
          {(invalidRelations || duplicateRelations) && <p role="alert" className="text-caption text-destructive">{collaborationT(($) => $.invalid_relations)}</p>}
          {(save.isError || changedRemotely) && <p role="alert" className="text-caption text-destructive">{saveConflict ? collaborationT(($) => $.save_conflict) : collaborationT(($) => $.save_failed)}</p>}
          {dirty && <Button size="sm" variant="outline" onClick={() => { setDirty(false); save.reset(); }}>{collaborationT(($) => $.discard_draft)}</Button>}
        </fieldset>
      )}
    </div>
  );
}
