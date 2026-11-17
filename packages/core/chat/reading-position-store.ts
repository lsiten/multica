"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { z } from "zod";
import { defaultStorage } from "../platform/storage";
import { registerDraftCleanup } from "../drafts/cleanup-registry";

const PositionSchema = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("live") }),
  z.object({ kind: z.literal("message"), messageId: z.string().min(1), offset: z.number().finite(), createdAt: z.string().optional() }),
]);
const PersistedSchema = z.object({ positions: z.record(z.string(), z.unknown()) });
export type ChatReadingPosition = Readonly<z.infer<typeof PositionSchema>>;

interface ChatReadingPositionState {
  readonly positions: Readonly<Record<string, ChatReadingPosition>>;
  readonly setPosition: (scope: string, position: ChatReadingPosition) => void;
}

export const useChatReadingPositionStore = create<ChatReadingPositionState>()(persist(
  (set) => ({
    positions: {},
    setPosition: (scope, position) => set((state) => {
      const previous = state.positions[scope];
      if (previous?.kind === "live" && position.kind === "live") return state;
      if (previous?.kind === "message" && position.kind === "message" && previous.messageId === position.messageId && previous.offset === position.offset && previous.createdAt === position.createdAt) return state;
      return { positions: { ...state.positions, [scope]: position } };
    }),
  }),
  {
    name: "multica_chat_reading_positions",
    storage: createJSONStorage(() => defaultStorage),
    partialize: (state) => ({ positions: state.positions }),
    merge: (persisted, current) => {
      const parsed = PersistedSchema.safeParse(persisted);
      if (!parsed.success) return current;
      const positions = Object.fromEntries(Object.entries(parsed.data.positions).flatMap(([scope, value]) => {
        const position = PositionSchema.safeParse(value);
        return scope && position.success ? [[scope, position.data]] : [];
      }));
      return { ...current, positions };
    },
  },
));

registerDraftCleanup({ storageKey: "multica_chat_reading_positions", workspaceScoped: false, resetInMemory: () => useChatReadingPositionStore.setState({ positions: {} }) });
