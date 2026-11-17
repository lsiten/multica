"use client";

import { create } from "zustand";
import type { ChatAutonomyPolicyOverride } from "../types";

interface ChatRunPolicyState {
  readonly policies: Readonly<Record<string, ChatAutonomyPolicyOverride | null>>;
  readonly setPolicy: (scope: string, policy: ChatAutonomyPolicyOverride | null) => void;
  readonly consumePolicy: (scope: string, sentPolicy: ChatAutonomyPolicyOverride | null) => void;
}

/** Upcoming-turn settings are shared between chat surfaces, never persisted. */
export const useChatRunPolicyStore = create<ChatRunPolicyState>()((set) => ({
  policies: {},
  setPolicy: (scope, policy) => set((state) => ({
    policies: { ...state.policies, [scope]: policy },
  })),
  consumePolicy: (scope, sentPolicy) => set((state) => {
    if (state.policies[scope] !== sentPolicy) return state;
    const { [scope]: _consumed, ...policies } = state.policies;
    return { policies };
  }),
}));
