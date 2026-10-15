"use client";

import { Plus, Users } from "lucide-react";
import type { Agent, ChatSession } from "@multica/core/types";
import { useAddChatGroupParticipant } from "@multica/core/chat/mutations";
import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useActorName } from "@multica/core/workspace/hooks";
import { cn } from "@multica/ui/lib/utils";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { Button } from "@multica/ui/components/ui/button";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";

/**
 * Who is in a group chat: a compact avatar cluster (+N overflow) that opens a
 * popover listing every member and the session agent by name. Direct (one-to-
 * one) chats have no participants, so this renders nothing for them.
 */
export function GroupChatParticipants({
  session,
  agents,
  className,
}: {
  session: ChatSession;
  agents: Agent[];
  className?: string;
}) {
  const { t } = useT("chat");
  const { getActorName } = useActorName();
  const participantsQuery = useQuery({
    queryKey: ["chat-group-participants", session.id],
    queryFn: () => api.listChatGroupParticipants(session.id),
    enabled: session.kind === "group" && !session.participants,
    staleTime: 30_000,
    retry: 2,
  });
  const participants = session.participants ?? participantsQuery.data ?? [];
  const addParticipant = useAddChatGroupParticipant();
  const participantIds = new Set(participants.filter((p) => p.member_type === "agent").map((p) => p.member_id));
  const availableAgents = agents.filter((candidate) => !participantIds.has(candidate.id) && candidate.id !== session.agent_id);

  if (session.kind !== "group") return null;

  return (
    <Popover>
      <PopoverTrigger
        render={
          <button
            type="button"
            className={cn(
              "inline-flex items-center gap-1 rounded-full px-1 py-0.5 text-micro text-muted-foreground transition-colors hover:text-foreground",
              className,
            )}
            aria-label={t(($) => $.group.open_members)}
          >
            <Users className="size-3 shrink-0" />
            <span className="tabular-nums">{participants.length}</span>
          </button>
        }
      />
      <PopoverContent align="start" className="w-56">
        <div className="mb-2 text-caption font-medium text-muted-foreground">
          {t(($) => $.group.participants, { count: participants.length })}
        </div>
        <ul className="space-y-1.5">
          {participants.map((p) => (
            <li
              key={p.member_type + p.member_id}
              className="flex items-center gap-2"
            >
              <ActorAvatar
                actorType={p.member_type}
                actorId={p.member_id}
                size="sm"
              />
              <span className="truncate text-body">{getActorName(p.member_type, p.member_id)}</span>
            </li>
          ))}
        </ul>
        {availableAgents.length > 0 && (
          <div className="mt-3 border-t pt-3">
            <div className="mb-1 text-caption font-medium text-muted-foreground">{t(($) => $.group.add_agent)}</div>
            <div className="max-h-32 space-y-1 overflow-y-auto">
              {availableAgents.map((candidate) => (
                <Button
                  key={candidate.id}
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="w-full justify-start gap-2"
                  disabled={addParticipant.isPending}
                  onClick={() => addParticipant.mutate({ sessionId: session.id, memberId: candidate.id })}
                >
                  <Plus className="size-3.5" />
                  <span className="truncate">{candidate.name}</span>
                </Button>
              ))}
            </div>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
