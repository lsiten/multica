import type { ChatReadingPosition } from "@multica/core/chat";

export interface ChatReadingRow {
  readonly messageId: string;
  readonly createdAt: string;
  readonly index: number;
}
export type ChatReadingAnchor = Extract<ChatReadingPosition, { kind: "message" }>;
export interface ChatReadingContext { readonly workspaceId: string; readonly sessionId: string; }

export function chatReadingScope(context: ChatReadingContext | undefined): string | null {
  return context?.workspaceId && context.sessionId ? JSON.stringify([context.workspaceId, context.sessionId]) : null;
}

export function chatScrollerVisible(element: HTMLElement): boolean {
  return element.clientHeight > 0 && element.clientWidth > 0 && element.getClientRects().length > 0;
}

export function captureChatReadingPosition(element: HTMLElement, rows: readonly ChatReadingRow[], following: boolean): ChatReadingPosition | null {
  if (!chatScrollerVisible(element)) return null;
  if (following) return { kind: "live" };
  const viewport = element.getBoundingClientRect();
  for (const node of element.querySelectorAll<HTMLElement>("[data-chat-message-id]")) {
    const box = node.getBoundingClientRect();
    if (box.bottom <= viewport.top || box.top >= viewport.bottom) continue;
    const row = rows.find((entry) => entry.messageId === node.dataset.chatMessageId);
    if (row) return { kind: "message", messageId: row.messageId, createdAt: row.createdAt, offset: viewport.top - box.top };
  }
  return null;
}

export function nearestChatReadingAnchor(rows: readonly ChatReadingRow[], target: ChatReadingAnchor): ChatReadingAnchor | null {
  const time = Date.parse(target.createdAt ?? "");
  let nearest = rows.at(-1);
  if (Number.isFinite(time)) {
    let distance = Infinity;
    for (const row of rows) {
      const candidate = Math.abs(Date.parse(row.createdAt) - time);
      if (candidate < distance) { distance = candidate; nearest = row; }
    }
  }
  return nearest ? { kind: "message", messageId: nearest.messageId, createdAt: nearest.createdAt, offset: 0 } : null;
}
