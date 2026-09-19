"use client";

import { useCallback, useMemo } from "react";
import { useT } from "../../../i18n";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useWSEvent } from "@multica/core/realtime";
import { memberListOptions } from "@multica/core/workspace/queries";
import {
  vscreenControlStateOptions,
  vscreenKeys,
} from "@multica/core/runtimes";
import type {
  MirrorSource,
  RuntimeMirrorControlState,
  VscreenScope,
  VscreenSourceDescriptor,
} from "@multica/core/types";

export interface MirrorControllerPresence {
  readonly viewerId: string;
  readonly userId: string;
  readonly name: string;
  readonly sourceLabel: string;
  readonly self: boolean;
}

function asControlState(
  payload: unknown,
  scope: VscreenScope,
): RuntimeMirrorControlState | null {
  if (!payload || typeof payload !== "object") return null;
  const value = payload as Record<string, unknown>;
  if (
    value.workspace_id !== scope.workspaceId ||
    value.runtime_id !== scope.runtimeId ||
    !Array.isArray(value.controllers)
  ) {
    return null;
  }
  const controllers = value.controllers.flatMap((controller) => {
    if (!controller || typeof controller !== "object") return [];
    const item = controller as Record<string, unknown>;
    const source = item.source;
    if (
      typeof item.viewer_id !== "string" ||
      typeof item.user_id !== "string" ||
      !source ||
      typeof source !== "object"
    ) {
      return [];
    }
    const sourceValue = source as Record<string, unknown>;
    const kind: MirrorSource["kind"] | unknown = sourceValue.kind;
    if (
      (kind !== "virtual" &&
        kind !== "physical" &&
        kind !== "system") ||
      typeof sourceValue.source_id !== "string"
    ) {
      return [];
    }
    const sourceKind = kind as MirrorSource["kind"];
    return [
      {
        viewerId: item.viewer_id as string,
        userId: item.user_id as string,
        source: {
          kind: sourceKind,
          sourceId: sourceValue.source_id as string,
        },
      },
    ];
  });
  return {
    workspaceId: scope.workspaceId,
    runtimeId: scope.runtimeId,
    controllers,
  };
}

export function useMirrorControllers({
  scope,
  enabled,
  catalog,
}: {
  readonly scope: VscreenScope;
  readonly enabled: boolean;
  readonly catalog: readonly VscreenSourceDescriptor[];
}) {
  const { t } = useT("runtimes");
  const queryClient = useQueryClient();
  const controlState = useQuery({
    ...vscreenControlStateOptions(scope),
    enabled,
  });
  const members = useQuery({
    ...memberListOptions(scope.workspaceId),
    enabled,
  });

  const handleControlEvent = useCallback(
    (payload: unknown) => {
      const state = asControlState(payload, scope);
      if (state) queryClient.setQueryData(vscreenKeys.controlState(scope), state);
    },
    [queryClient, scope],
  );
  useWSEvent("runtime_mirror:control", handleControlEvent);

  const sourceLabel = useCallback(
    (source: RuntimeMirrorControlState["controllers"][number]["source"]) => {
      const match = catalog.find(
        (entry) =>
          entry.source.kind === source.kind &&
          entry.source.sourceId === source.sourceId,
      );
      return match?.name.trim() || source.sourceId;
    },
    [catalog],
  );

  const controllers = useMemo<readonly MirrorControllerPresence[]>(() => {
    return (controlState.data?.controllers ?? []).map((controller) => {
      const self = controller.userId === scope.accountId;
      return {
        viewerId: controller.viewerId,
        userId: controller.userId,
        self,
        name: self
          ? ""
          : members.data?.find(
              (member) => member.user_id === controller.userId,
            )?.name || t(($) => $.vscreen.controller_unknown_member),
        sourceLabel: sourceLabel(controller.source),
      };
    });
  }, [
    controlState.data?.controllers,
    members.data,
    scope.accountId,
    sourceLabel,
    t,
  ]);

  return { controllers };
}
