"use client";

import { Users } from "lucide-react";
import type { ChatSession } from "@multica/core/types";
import { useActorName } from "@multica/core/workspace/hooks";
import { cn } from "@multica/ui/lib/utils";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";

/**
 * Who is in a group chat: a compact avatar cluster (+N overflow) that opens a
 * popover listing every member and the session agent by name. Direct (one-to-
 * one) chats have no participants, so this renders nothing for them.
 */
export function GroupChatParticipants({
  session,
  className,
}: {
  session: ChatSession;
  className?: string;
}) {
  const { t } = useT("chat");
  const { getActorName } = useActorName();
  const participants = session.participants ?? [];

  if (participants.length === 0) return null;

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
      </PopoverContent>
    </Popover>
  );
}
