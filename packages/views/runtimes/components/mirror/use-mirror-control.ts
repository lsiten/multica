"use client";

import { useCallback, useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { getApi } from "@multica/core/api";
import type {
  AgentDesktopControlInput,
  AgentDesktopControlStatus,
  VscreenScope,
} from "@multica/core/types";

export function useMirrorControl(scope: VscreenScope, enabled: boolean) {
  const [actionPending, setActionPending] = useState(false);
  const [error, setError] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const api = getApi().vscreen(scope);
  const status = useQuery<AgentDesktopControlStatus>({
    queryKey: [
      "agent-desktop-control",
      scope.accountId,
      scope.workspaceId,
      scope.runtimeId,
    ],
    queryFn: ({ signal }) => api.getAgentDesktopControl(signal),
    enabled,
    retry: false,
    refetchInterval: (query) => {
      const state = query.state.data?.state;
      return state === "waiting_for_approval" || state === "approval_pending" || state === "controlling"
        ? 2_000
        : false;
    },
  });
  useEffect(() => {
    if (status.data?.state !== "controlling") return;
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, [status.data?.state]);
  useEffect(() => {
    const stop = () => {
      if (status.data?.state === "controlling") void api.stopAgentDesktopControl();
    };
    window.addEventListener("pagehide", stop);
    return () => window.removeEventListener("pagehide", stop);
  }, [api, status.data?.state]);

  const run = useCallback(async (operation: () => Promise<AgentDesktopControlStatus>) => {
    setActionPending(true);
    setError(false);
    try {
      const next = await operation();
      await status.refetch();
      return next;
    } catch (reason) {
      if (reason instanceof Error) setError(true);
      else throw reason;
      return null;
    } finally {
      setActionPending(false);
    }
  }, [status]);

  const request = useCallback(
    () => run(() => api.requestAgentDesktopControl()),
    [api, run],
  );
  const approve = useCallback(
    (requestId: string) => run(() => api.approveAgentDesktopControl(requestId)),
    [api, run],
  );
  const deny = useCallback(async (requestId: string) => {
    setActionPending(true);
    setError(false);
    try {
      await api.denyAgentDesktopControl(requestId);
      await status.refetch();
    } catch (reason) {
      if (reason instanceof Error) setError(true);
      else throw reason;
    } finally {
      setActionPending(false);
    }
  }, [api, status]);
  const send = useCallback(async (input: AgentDesktopControlInput) => {
    setError(false);
    try {
      return await api.sendAgentDesktopControlInput(input);
    } catch (reason) {
      if (reason instanceof Error) {
        setError(true);
        await status.refetch();
      } else {
        throw reason;
      }
      return null;
    }
  }, [api, status]);
  const stop = useCallback(async () => {
    setActionPending(true);
    setError(false);
    try {
      await api.stopAgentDesktopControl();
      await status.refetch();
    } catch (reason) {
      if (reason instanceof Error) setError(true);
      else throw reason;
    } finally {
      setActionPending(false);
    }
  }, [api, status]);
  const leaseSeconds = status.data?.leaseExpiresAt
    ? Math.max(0, Math.ceil((Date.parse(status.data.leaseExpiresAt) - now) / 1_000))
    : null;
  return {
    status: status.data,
    pending: status.isPending || actionPending,
    error: error || status.isError,
    leaseSeconds,
    request,
    approve,
    deny,
    send,
    stop,
    refetch: status.refetch,
  };
}
