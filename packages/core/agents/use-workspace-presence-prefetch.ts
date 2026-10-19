"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { agentListOptions, squadListOptions } from "../workspace/queries";
import { runtimeListOptions } from "../runtimes/queries";
import { agentTaskSnapshotOptions } from "./queries";

// Subscribe to the queries that power agent presence and the @mention
// suggestion list so they're warm by the time any hover card / inline
// indicator / mention popup first renders. Without this warm-up, surfaces
// that don't otherwise touch the snapshot (inbox, issues, chat) flash a
// skeleton on first hover while the fetch is in flight, and the @mention
// list may show incomplete results (e.g. missing squads).
//
// useRealtimeSync (WS task / agent / daemon / squad invalidations) and the
// 30s presence tick keep these caches fresh after the initial fetch — this
// hook only collapses the cold-start window.
//
// All queries are workspace-scoped; the queryKeys include wsId so workspace
// switch automatically refetches the new workspace's data with no extra
// wiring here. The workspace-scoped layouts on both apps gate rendering on
// "workspace resolved", so callers can safely pass useWorkspaceId() — by the
// time this hook mounts, wsId is guaranteed non-empty.
export function useWorkspacePresencePrefetch(wsId: string | undefined): void {
  const [isIdle, setIsIdle] = useState(false);

  useEffect(() => {
    setIsIdle(false);
    if (!wsId || typeof window === "undefined") return;

    let cancelled = false;
    const run = () => {
      if (!cancelled) setIsIdle(true);
    };
    const idleWindow = window as unknown as {
      requestIdleCallback?: (callback: () => void, options?: { timeout: number }) => number;
      cancelIdleCallback?: (handle: number) => void;
    };
    const requestIdleCallback = idleWindow.requestIdleCallback;
    const cancelIdleCallback = idleWindow.cancelIdleCallback;
    const idleHandle = requestIdleCallback
      ? requestIdleCallback(run, { timeout: 1_000 })
      : window.setTimeout(run, 1_000);

    return () => {
      cancelled = true;
      if (cancelIdleCallback && requestIdleCallback) {
        cancelIdleCallback(idleHandle as number);
      } else {
        window.clearTimeout(idleHandle as number);
      }
    };
  }, [wsId]);

  // Presence is useful for hover cards and mention pickers, but it is not a
  // prerequisite for rendering the current page. Start the four warm-up
  // requests after the first idle window so they cannot compete with the
  // route's critical data on a slow backend.
  const enabled = !!wsId && isIdle;
  useQuery({ ...agentListOptions(wsId ?? ""), enabled });
  useQuery({ ...runtimeListOptions(wsId ?? ""), enabled });
  useQuery({ ...agentTaskSnapshotOptions(wsId ?? ""), enabled });
  useQuery({ ...squadListOptions(wsId ?? ""), enabled });
}
