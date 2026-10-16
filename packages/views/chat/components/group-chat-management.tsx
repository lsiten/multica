"use client";

import { useEffect, useMemo, useState } from "react";
import { Check, Pencil, Save, UserMinus, Users } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import type { Agent, ChatParticipant, ChatSession } from "@multica/core/types";
import { api } from "@multica/core/api";
import {
  useAddChatGroupParticipant,
  useRemoveChatGroupParticipant,
  useUpdateChatSession,
} from "@multica/core/chat/mutations";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useActorName } from "@multica/core/workspace/hooks";
import { useT } from "../../i18n";
import { ActorAvatar } from "../../common/actor-avatar";

export function GroupChatManagement({ session, agents }: { session: ChatSession; agents: Agent[] }) {
  const { t } = useT("chat");
  const { getActorName } = useActorName();
  const [title, setTitle] = useState(session.title ?? "");
  const [editingTitle, setEditingTitle] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const participantsQuery = useQuery({
    queryKey: ["chat-group-participants", session.id],
    queryFn: () => api.listChatGroupParticipants(session.id),
    enabled: session.kind === "group",
    staleTime: 30_000,
    retry: 2,
  });
  const participants = useMemo(
    () => participantsQuery.data ?? session.participants ?? [],
    [participantsQuery.data, session.participants],
  );
  const updateSession = useUpdateChatSession();
  const addParticipant = useAddChatGroupParticipant();
  const removeParticipant = useRemoveChatGroupParticipant();
  const participantKeys = useMemo(
    () => new Set(participants.map((participant) => `${participant.member_type}:${participant.member_id}`)),
    [participants],
  );
  const availableAgents = useMemo(() => {
    const query = filter.trim().toLowerCase();
    return agents.filter((agent) =>
      agent.id !== session.agent_id &&
      !participantKeys.has(`agent:${agent.id}`) &&
      (!query || agent.name.toLowerCase().includes(query)),
    );
  }, [agents, filter, participantKeys, session.agent_id]);

  useEffect(() => {
    setTitle(session.title ?? "");
  }, [session.title]);

  if (session.kind !== "group") return null;

  const saveTitle = () => {
    const next = title.trim();
    if (!next || next === session.title) {
      setTitle(session.title ?? "");
      setEditingTitle(false);
      return;
    }
    updateSession.mutate({ sessionId: session.id, title: next });
    setEditingTitle(false);
  };

  const addSelected = async () => {
    for (const memberId of selectedIds) {
      await addParticipant.mutateAsync({ sessionId: session.id, memberId });
    }
    setSelectedIds([]);
    setFilter("");
    setAddOpen(false);
  };

  const renderParticipant = (participant: ChatParticipant) => {
    const isLead = participant.member_type === "agent" && participant.member_id === session.agent_id;
    return (
      <li key={`${participant.member_type}:${participant.member_id}`} className="flex items-center gap-2 rounded-md px-2 py-2 hover:bg-muted/50">
        <ActorAvatar actorType={participant.member_type} actorId={participant.member_id} size="sm" />
        <span className="min-w-0 flex-1 truncate text-body">{getActorName(participant.member_type, participant.member_id)}</span>
        {isLead ? (
          <span className="shrink-0 text-micro text-muted-foreground">{t(($) => $.group.lead_agent)}</span>
        ) : (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="shrink-0 text-muted-foreground hover:text-destructive"
            disabled={removeParticipant.isPending}
            onClick={() => removeParticipant.mutate({ sessionId: session.id, memberType: participant.member_type, memberId: participant.member_id })}
            aria-label={t(($) => $.group.remove_member)}
            title={t(($) => $.group.remove_member)}
          >
            <UserMinus className="size-4" />
          </Button>
        )}
      </li>
    );
  };

  return (
    <div className="flex h-full min-h-0 flex-col overflow-y-auto">
      <div className="border-b px-3 py-3">
        <div className="mb-2 flex items-center gap-2 text-caption font-medium text-muted-foreground">
          <Users className="size-4" />
          {t(($) => $.group.management_title)}
        </div>
        <div className="flex items-center gap-2">
          {editingTitle ? (
            <Input
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              onKeyDown={(event) => { if (event.key === "Enter") saveTitle(); if (event.key === "Escape") { setTitle(session.title ?? ""); setEditingTitle(false); } }}
              aria-label={t(($) => $.group.group_name)}
              autoFocus
              className="h-8 min-w-0"
            />
          ) : (
            <p className="min-w-0 flex-1 truncate text-body font-semibold" title={session.title}>{session.title || t(($) => $.window.untitled)}</p>
          )}
          {editingTitle ? (
            <Button type="button" size="icon-sm" variant="ghost" onClick={saveTitle} disabled={updateSession.isPending} aria-label={t(($) => $.group.save_name)}>
              <Save className="size-4" />
            </Button>
          ) : (
            <Button type="button" size="icon-sm" variant="ghost" onClick={() => setEditingTitle(true)} aria-label={t(($) => $.group.edit_name)}>
              <Pencil className="size-4" />
            </Button>
          )}
        </div>
        <p className="mt-1 text-caption text-muted-foreground">
          {t(($) => $.group.lead_agent)} · {getActorName("agent", session.agent_id)}
        </p>
      </div>

      <section className="px-3 py-3" aria-labelledby="group-members-heading">
        <div className="mb-2 flex items-center justify-between gap-2">
          <h3 id="group-members-heading" className="text-caption font-medium text-muted-foreground">
            {t(($) => $.group.members)} · {participants.length}
          </h3>
          <Button type="button" size="sm" variant="outline" className="h-7 gap-1.5" onClick={() => setAddOpen(true)}>
            <Users className="size-3.5" />
            {t(($) => $.group.add_agents)}
          </Button>
        </div>
        {participants.length > 0 ? (
          <ul className="space-y-1">{participants.map(renderParticipant)}</ul>
        ) : (
          <p className="py-3 text-body text-muted-foreground">{t(($) => $.group.no_members)}</p>
        )}
      </section>

      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>{t(($) => $.group.add_agents)}</DialogTitle>
            <DialogDescription>{t(($) => $.group.select_agents)}</DialogDescription>
          </DialogHeader>
          <Input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder={t(($) => $.window.agent_filter_placeholder)} aria-label={t(($) => $.window.agent_filter_placeholder)} />
          <div className="max-h-72 space-y-1 overflow-y-auto">
            {availableAgents.length === 0 ? (
              <p className="py-4 text-body text-muted-foreground">{t(($) => $.group.no_available_agents)}</p>
            ) : availableAgents.map((agent) => {
              const selected = selectedIds.includes(agent.id);
              return (
                <button
                  key={agent.id}
                  type="button"
                  className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left hover:bg-muted/60"
                  onClick={() => setSelectedIds((ids) => selected ? ids.filter((id) => id !== agent.id) : [...ids, agent.id])}
                  aria-pressed={selected}
                >
                  <ActorAvatar actorType="agent" actorId={agent.id} size="sm" />
                  <span className="min-w-0 flex-1 truncate text-body">{agent.name}</span>
                  {selected && <Check className="size-4 text-brand" />}
                </button>
              );
            })}
          </div>
          <DialogFooter>
            <Button type="button" disabled={selectedIds.length === 0 || addParticipant.isPending} onClick={() => void addSelected()}>
              {t(($) => $.group.add_selected)}{selectedIds.length > 0 ? ` (${selectedIds.length})` : ""}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
