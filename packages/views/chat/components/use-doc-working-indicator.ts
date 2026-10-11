"use client";

import { useEffect } from "react";
import { useDocModeStore, selectActiveDocument } from "@multica/core/chat";

// D7 (confirmed v12): while a document is open in doc mode, the floating layer
// shows ONLY the "working" status — a generic "Agent 工作中…" indicator (spinner
// + progress), not the conversation content. The confirmed mockup is a generic
// agent-working indicator, not a strict per-document signal, so this hook
// bridges the agent's pending task with doc mode: it sets `working` when a
// document is open AND the agent is working, and clears it when the agent
// finishes. Clearing `working` never closes doc mode (D9) — only an explicit
// `closeDoc` returns to the conversation, so an edit completing does not
// auto-switch away.
export function useDocWorkingIndicator(wsId: string, isAgentWorking: boolean) {
  const activeDoc = useDocModeStore(selectActiveDocument(wsId));
  const setWorking = useDocModeStore((s) => s.setWorking);
  useEffect(() => {
    setWorking(wsId, !!activeDoc && isAgentWorking);
  }, [activeDoc, isAgentWorking, wsId, setWorking]);
}
