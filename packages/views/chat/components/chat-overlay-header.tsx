"use client";

import type { RefObject } from "react";
import { ChevronDown, Columns2, GripVertical, Minus } from "@multica/ui/icons";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { useChatWorkspaceLayoutStore } from "@multica/core/chat";
import { useT } from "../../i18n";
import { useChatOverlayDrag } from "./use-chat-overlay-drag";

export function ChatOverlayHeader({ scope, container, visible, collapsed, title }: {
  readonly scope: string;
  readonly container: RefObject<HTMLDivElement | null>;
  readonly visible: boolean;
  readonly collapsed: boolean;
  readonly title: string;
}) {
  const { t } = useT("chat");
  const drag = useChatOverlayDrag(scope, container);
  const setLayout = useChatWorkspaceLayoutStore((state) => state.setLayout);
  return (
    <div hidden={!visible} className="flex h-10 shrink-0 items-center gap-1 border-b px-2">
      <button type="button" aria-label={t(($) => $.workspace.move_chat)} title={title}
        onPointerDown={drag.onPointerDown} onKeyDown={drag.onKeyDown}
        className="flex h-8 min-w-0 flex-1 cursor-move touch-none items-center gap-2 rounded-md px-2 text-label outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <GripVertical className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="truncate">{title}</span>
      </button>
      <IconButton label={t(($) => collapsed ? $.workspace.expand_chat : $.workspace.collapse_chat)} aria-expanded={!collapsed} onClick={drag.toggleCollapsed}>
        {collapsed ? <ChevronDown /> : <Minus />}
      </IconButton>
      <IconButton label={t(($) => $.workspace.split_document)} onClick={() => setLayout(scope, { mode: "parallel" })}><Columns2 /></IconButton>
    </div>
  );
}
