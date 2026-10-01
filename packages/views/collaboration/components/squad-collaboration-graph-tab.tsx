"use client";
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
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
    const [from, to] = members;
    if (!from || !to) return;
    setRelations((current) => [...current, {
      id: `draft-${Date.now()}`,
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
  const changedRemotely = dirty && graph?.revision !== baseRevision;
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
      <CollaborationGraphCanvas
        nodes={displayedMembers.map((member) => ({ id: `${member.member_type}:${member.member_id}`, type: "agent" as const, label: member.label, data: { role: member.role } }))}
        edges={[
          ...derivedRelations.map((relation) => ({ id: `derived:${relation.id}`, from: `${relation.from_member_type}:${relation.from_member_id}`, to: `${relation.to_member_type}:${relation.to_member_id}`, type: relation.type, count: 1, active_count: 0, evidence_count: 0, last_event_at: null })),
          ...displayedRelations.map((relation) => ({ id: relation.id, from: `${relation.from_member_type}:${relation.from_member_id}`, to: `${relation.to_member_type}:${relation.to_member_id}`, type: relation.type, count: 1, active_count: 0, evidence_count: 0, last_event_at: null })),
        ]}
        emptyLabel={collaborationT(($) => $.empty_squad)}
      />
      {(!canManage || historyRevision !== null) && displayedRelations.map((relation) => (
        <article key={relation.id} className="space-y-1 rounded-md border p-3 text-caption">
          <h4 className="font-medium">{relation.label || relation.type}</h4>
          {relation.trigger && <p>{relation.trigger}</p>}
          {relation.deliverables.length > 0 && <ul className="list-disc pl-4">{relation.deliverables.map((item, index) => <li key={index}>{item}</li>)}</ul>}
          {relation.acceptance && <p>{relation.acceptance}</p>}
        </article>
      ))}
      {canManage && historyRevision === null && (
        <fieldset disabled={save.isPending} className="space-y-2 rounded-md border p-3">
          <div className="flex items-center justify-between gap-2"><span className="text-caption font-medium">{collaborationT(($) => $.relations_title)}</span><Button size="sm" variant="outline" onClick={addRelation} disabled={save.isPending || members.length < 2}>{collaborationT(($) => $.add_relation)}</Button></div>
          {relations.map((relation) => (
            <div key={relation.id} className="space-y-2 rounded-md bg-muted/30 p-2">
              <div className="grid grid-cols-2 gap-2">
                <select aria-label={collaborationT(($) => $.from_member)} className="h-8 rounded-md border bg-background px-2 text-caption" value={`${relation.from_member_type}:${relation.from_member_id}`} onChange={(event) => { const [type, id] = event.target.value.split(":"); updateRelation(relation.id, { from_member_type: type as "agent" | "member", from_member_id: id }); }}>
                  {members.map((member) => <option key={`${member.member_type}:${member.member_id}`} value={`${member.member_type}:${member.member_id}`}>{member.label}</option>)}
                </select>
                <select aria-label={collaborationT(($) => $.to_member)} className="h-8 rounded-md border bg-background px-2 text-caption" value={`${relation.to_member_type}:${relation.to_member_id}`} onChange={(event) => { const [type, id] = event.target.value.split(":"); updateRelation(relation.id, { to_member_type: type as "agent" | "member", to_member_id: id }); }}>
                  {members.map((member) => <option key={`${member.member_type}:${member.member_id}`} value={`${member.member_type}:${member.member_id}`}>{member.label}</option>)}
                </select>
              </div>
              <div className="grid grid-cols-2 gap-2">
                <select aria-label={collaborationT(($) => $.relation_type)} className="h-8 rounded-md border bg-background px-2 text-caption" value={relation.type} onChange={(event) => updateRelation(relation.id, { type: event.target.value as SquadCollaborationRelation["type"] })}>
                  <option value="coordinate">{collaborationT(($) => $.coordinate)}</option><option value="handoff">{collaborationT(($) => $.handoff)}</option><option value="review">{collaborationT(($) => $.review)}</option><option value="accept">{collaborationT(($) => $.accept)}</option>
                </select>
                <input className="h-8 rounded-md border bg-background px-2 text-caption" value={relation.label} placeholder={collaborationT(($) => $.relation_label)} onChange={(event) => updateRelation(relation.id, { label: event.target.value })} />
              </div>
              <input className="h-8 w-full rounded-md border bg-background px-2 text-caption" value={relation.trigger} placeholder={collaborationT(($) => $.trigger)} onChange={(event) => updateRelation(relation.id, { trigger: event.target.value })} />
              <textarea aria-label={collaborationT(($) => $.deliverables)} className="min-h-14 w-full rounded-md border bg-background px-2 py-1 text-caption" value={relation.deliverables.join("\n")} placeholder={collaborationT(($) => $.deliverables)} onChange={(event) => updateRelation(relation.id, { deliverables: event.target.value.split("\n") })} />
              <textarea className="min-h-14 w-full rounded-md border bg-background px-2 py-1 text-caption" value={relation.acceptance} placeholder={collaborationT(($) => $.acceptance)} onChange={(event) => updateRelation(relation.id, { acceptance: event.target.value })} />
              <Button size="sm" variant="ghost" className="text-destructive" onClick={() => removeRelation(relation.id)}>{collaborationT(($) => $.delete)}</Button>
            </div>
          ))}
          <Button size="sm" onClick={() => save.mutate()} disabled={save.isPending || !dirty || !graph}>{save.isPending ? collaborationT(($) => $.saving) : collaborationT(($) => $.save)}</Button>
          {(save.isError || changedRemotely) && <p role="alert" className="text-caption text-destructive">{collaborationT(($) => $.save_conflict)}</p>}
          {dirty && <Button size="sm" variant="outline" onClick={() => { setDirty(false); save.reset(); }}>{collaborationT(($) => $.discard_draft)}</Button>}
        </fieldset>
      )}
    </div>
  );
}
