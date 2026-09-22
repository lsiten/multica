"use client";
import { createContext, useContext, useSyncExternalStore } from "react";
import type { LocalVoiceAdapter, LocalVoiceStatus } from "@multica/core/platform";
export const LocalVoiceContext = createContext<LocalVoiceAdapter | null>(null);
const noSubscribe = () => () => {};
const noStatus = (): LocalVoiceStatus | null => null;
export function useLocalVoice() {
  const adapter = useContext(LocalVoiceContext);
  const status = useSyncExternalStore(adapter?.subscribe ?? noSubscribe, adapter?.getSnapshot ?? noStatus, noStatus);
  return { adapter, status };
}
