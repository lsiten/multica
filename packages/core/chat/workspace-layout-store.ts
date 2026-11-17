"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { z } from "zod";
import { defaultStorage } from "../platform/storage";

const LayoutSchema = z.object({
  mode: z.enum(["parallel", "focused"]),
  conversationPercent: z.number().min(30).max(70),
  overlay: z.object({ x: z.number().nonnegative(), y: z.number().nonnegative(), collapsed: z.boolean() }).optional(),
});
const PersistedLayoutSchema = z.object({
  layouts: z.record(z.string(), LayoutSchema),
  readingPositions: z.record(z.string(), z.number().nonnegative()).optional().default({}),
});

export type ChatWorkspaceLayout = Readonly<z.infer<typeof LayoutSchema>>;
export const DEFAULT_CHAT_WORKSPACE_LAYOUT: ChatWorkspaceLayout = {
  mode: "parallel", conversationPercent: 45,
};

interface ChatWorkspaceLayoutState {
  readonly layouts: Readonly<Record<string, ChatWorkspaceLayout>>;
  readonly readingPositions: Readonly<Record<string, number>>;
  readonly setReadingPosition: (scope: string, position: number) => void;
  readonly setLayout: (scope: string, update: Partial<ChatWorkspaceLayout>) => void;
}

export const useChatWorkspaceLayoutStore = create<ChatWorkspaceLayoutState>()(persist(
  (set) => ({
    layouts: {},
    readingPositions: {},
    setReadingPosition: (scope, position) => set((state) => ({ readingPositions: { ...state.readingPositions, [scope]: Math.max(0, position) } })),
    setLayout: (scope, update) => set((state) => {
      const current = state.layouts[scope] ?? DEFAULT_CHAT_WORKSPACE_LAYOUT;
      const next = {
        ...current, ...update,
        conversationPercent: Math.max(30, Math.min(70, update.conversationPercent ?? current.conversationPercent)),
      };
      if (current.mode === next.mode && current.conversationPercent === next.conversationPercent && current.overlay === next.overlay) return state;
      return { layouts: { ...state.layouts, [scope]: next } };
    }),
  }),
  {
    name: "multica_chat_workspace_layout",
    storage: createJSONStorage(() => defaultStorage),
    partialize: (state) => ({ layouts: state.layouts, readingPositions: state.readingPositions }),
    merge: (persisted, current) => {
      const parsed = PersistedLayoutSchema.safeParse(persisted);
      return parsed.success ? { ...current, ...parsed.data } : current;
    },
  },
));
