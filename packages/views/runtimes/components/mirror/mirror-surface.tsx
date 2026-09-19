"use client";
import { MirrorHandoff } from "./mirror-handoff";
import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Monitor, PictureInPicture2, RefreshCw } from "lucide-react";
import type {
  RuntimeDevice,
  VscreenCommandKind,
  VscreenScope,
} from "@multica/core/types";
import {
  deriveVscreenAccess,
  runtimeDisplayLabel,
  useVscreenCommand,
  vscreenCommandOptions,
  vscreenKeys,
  vscreenSourcesOptions,
  vscreenStateOptions,
} from "@multica/core/runtimes";
import { getApi, vscreenErrorReason } from "@multica/core/api";
import { agentListOptions } from "@multica/core/workspace";
import { chatSessionsOptions } from "@multica/core/chat/queries";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../../i18n";
import { useVideoSession } from "./use-video-session";
import { MirrorVideo } from "./mirror-video";
import { useMirrorPlatform } from "./mirror-platform";
import { MirrorSourcePicker, mirrorSourceKey } from "./mirror-source-picker";
import { MirrorCommandControls } from "./mirror-command-controls";
import { MirrorControlBar } from "./mirror-control-bar";
import { InteractiveMirrorVideo } from "./interactive-mirror-video";
import { MirrorControllerPresence } from "./mirror-controller-presence";
import { MirrorAuthorizationPrompt } from "./mirror-authorization-prompt";

export function MirrorSurface({
  scope,
  runtime,
  compact = false,
}: {
  readonly scope: VscreenScope;
  readonly runtime: RuntimeDevice;
  readonly compact?: boolean;
}) {
  const { t } = useT("runtimes");
  const platform = useMirrorPlatform();
  const queryClient = useQueryClient();
  const readable =
    !!runtime.owner_id &&
    (runtime.owner_id === scope.accountId || runtime.visibility === "public");
  const online = runtime.status === "online" && !!runtime.daemon_id;
  const state = useQuery({
    ...vscreenStateOptions(scope, queryClient),
    enabled: online && readable,
  });
  const agents = useQuery({ ...agentListOptions(scope.workspaceId), enabled: online && readable });
  const sessions = useQuery({ ...chatSessionsOptions(scope.workspaceId), enabled: online && readable });
  const sources = useQuery({
    ...vscreenSourcesOptions(scope),
    enabled: online && readable,
  });
  const [selected, setSelected] = useState<string>("");
  const initialSourceChosen = useRef(false);
  const previousSelectedSource = useRef<string | null>(null);
  const [retry, setRetry] = useState(0);
  const [floatingError, setFloatingError] = useState(false);
  const [commandId, setCommandId] = useState<string | null>(null);
  const command = useVscreenCommand(scope);
  const receipt = useQuery({
    ...vscreenCommandOptions(scope, commandId ?? ""),
    enabled: commandId !== null && !command.isPending,
  });
  const catalog = sources.isError ? [] : (sources.data?.sources ?? []);
  useEffect(() => {
    if (initialSourceChosen.current || !sources.isSuccess || !state.isSuccess)
      return;
    initialSourceChosen.current = true;
    const initial =
      state.data?.state.state === "ready"
        ? sources.data?.sources.find((entry) => entry.source.kind === "virtual")
        : undefined;
    if (initial) setSelected(mirrorSourceKey(initial));
  }, [sources.data, sources.isSuccess, state.data, state.isSuccess]);
  const source = catalog.find(
    (binding) => mirrorSourceKey(binding) === selected,
  );
  const captureDenied = ["denied", "restricted", "not_determined"].includes(
    state.data?.state.permissions.screenRecording ?? "",
  );
  const binding =
    online && readable && !captureDenied && source ? source : null;
  const video = useVideoSession(scope, binding, retry);
  const access = deriveVscreenAccess({
    scope,
    runtime,
    authorization: {
      canReadRuntime: readable,
      canManageRuntime: runtime.owner_id === scope.accountId,
    },
    observation: state.isError ? null : state.data,
  });
  const commandUncertain =
    !!commandId && (receipt.isError || receipt.data === null);
  const commandPending =
    command.isPending ||
    (!!commandId &&
      (receipt.isPending ||
        ["pending", "running"].includes(receipt.data?.state ?? "")));
  useEffect(() => {
    if (receipt.data && !["pending", "running"].includes(receipt.data.state))
      void queryClient.invalidateQueries({ queryKey: vscreenKeys.all(scope) });
    // Identity is already included in the receipt query key.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [receipt.data?.state, queryClient]);
  const runCommand = (kind: VscreenCommandKind) => {
    const id = crypto.randomUUID();
    setCommandId(id);
    command.mutate(
      { commandId: id, kind },
      {
        onSuccess: async () => {
          if (kind !== "emergency_stop") return;
          await video.stopControl();
          await queryClient.invalidateQueries({
            queryKey: vscreenKeys.all(scope),
          });
        },
      },
    );
  };
  const reason =
    video.reason ??
    vscreenErrorReason(sources.error) ??
    vscreenErrorReason(state.error);
  const status = !readable
    ? t(($) => $.vscreen.access_denied)
    : !online || reason === "daemon_unavailable" || reason === "offline"
      ? t(($) => $.vscreen.offline)
      : captureDenied ||
          reason === "permission_denied" ||
          reason === "permission-denied"
        ? t(($) => $.vscreen.permission_denied)
        : reason === "native_unavailable" ||
            reason === "capture-unavailable" ||
            reason === "capture_unavailable"
          ? t(($) => $.vscreen.capture_unavailable)
          : reason === "upgrade_required"
            ? t(($) => $.vscreen.upgrade)
            : (selected && !source) ||
                reason === "source_gone" ||
                reason === "source_unavailable" ||
                reason === "no-display"
              ? t(($) => $.vscreen.source_gone)
              : sources.isPending || video.state === "preparing"
                ? t(($) => $.vscreen.preparing)
                : sources.isError || video.state === "failed"
                  ? t(($) => $.vscreen.failed)
                  : !catalog.length
                    ? t(($) => $.vscreen.empty)
                    : !selected
                      ? t(($) => $.vscreen.choose_source)
                      : video.state === "streaming"
                        ? t(($) => $.vscreen.streaming)
                        : t(($) => $.vscreen.negotiating);
  const label = runtimeDisplayLabel(runtime);
  const quality = video.quality ?? video.metadata?.quality;
  const controlActive = video.control.status === "active";
  const sendType = (text: string) =>
    video.sendInput({
      kind: "type",
      gestureId: crypto.randomUUID(),
      text: { text },
    });
  const sendKey = (
    key: string,
    modifiers: ("shift" | "control" | "alt" | "meta")[] = [],
  ) => {
    const gestureId = `key-${crypto.randomUUID()}`;
    video.sendInput({
      kind: "key:down",
      gestureId,
      key: { key, modifiers },
    });
    video.sendInput({
      kind: "key:up",
      gestureId,
      key: { key, modifiers },
    });
  };
  const sendAgentMessage = async (agentId: string, text: string) => {
    const session = sessions.data?.find((candidate) => candidate.agent_id === agentId && candidate.status !== "archived");
    const target = session ?? await getApi().createChatSession({ agent_id: agentId });
    await getApi().sendChatMessage(target.id, text);
    void sessions.refetch();
  };
  useEffect(() => {
    if (previousSelectedSource.current === null) {
      previousSelectedSource.current = selected;
      return;
    }
    if (previousSelectedSource.current !== selected) {
      previousSelectedSource.current = selected;
      if (video.control.status === "active") void video.stopControl();
    }
    // Source switches must replace the control grant rather than rebind it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected]);
  return (
    <section
      aria-label={t(($) => $.mirror.frame_label)}
      className="flex min-h-0 flex-col overflow-hidden rounded-lg border bg-card"
    >
      <div className="flex flex-wrap items-center gap-2 border-b p-2">
        <MirrorSourcePicker
          catalog={catalog}
          selected={selected}
          enabled={online && readable && !sources.isPending}
          compact={compact}
          onSelect={(key) => {
            initialSourceChosen.current = true;
            setSelected(key);
          }}
        />
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={t(($) => $.vscreen.retry)}
          onClick={() => {
            void sources.refetch();
            setRetry((value) => value + 1);
          }}
        >
          <RefreshCw aria-hidden="true" />
        </Button>
        {!compact && platform.openFloating && (
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={t(($) => $.vscreen.float)}
            onClick={async () => {
              try {
                await platform.openFloating?.(scope, label);
                setFloatingError(false);
              } catch (error) {
                if (error instanceof Error) setFloatingError(true);
                else throw error;
              }
            }}
          >
            <PictureInPicture2 aria-hidden="true" />
          </Button>
        )}
      </div>
      {floatingError && (
        <p className="px-3 py-2 text-caption text-destructive" role="alert">
          {t(($) => $.vscreen.floating_failed)}
        </p>
      )}
      <MirrorControllerPresence
        scope={scope}
        enabled={online && readable}
        catalog={catalog}
      />
      <div className="relative aspect-video min-h-0 w-full bg-muted/30">
        {video.authorization && (
          <MirrorAuthorizationPrompt
            request={video.authorization}
            onDecision={(approved) => {
              video.respondAuthorization(video.authorization?.request_id ?? "", approved);
            }}
          />
        )}
        {video.stream && binding ? (
          controlActive ? (
            <InteractiveMirrorVideo
              stream={video.stream}
              onFrame={video.onFrame}
              source={binding}
              active
              sendInput={video.sendInput}
              label={t(($) => $.mirror.frame_alt, { runtime: label })}
            />
          ) : (
            <MirrorVideo
              stream={video.stream}
              onFrame={video.onFrame}
              ready={video.state === "streaming"}
              label={t(($) => $.mirror.frame_alt, { runtime: label })}
            />
          )
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-3 px-4 text-center">
            <Monitor
              aria-hidden="true"
              className="size-6 text-muted-foreground"
            />
            <p
              className="max-w-md text-caption text-muted-foreground"
              role="status"
            >
              {status}
            </p>
          </div>
        )}
        {video.stream && video.state !== "streaming" && (
          <div
            className="absolute inset-0 flex items-center justify-center px-4 text-caption text-muted-foreground"
            role="status"
          >
            {status}
          </div>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2 border-t px-3 py-2 text-caption text-muted-foreground">
        <span role={video.state === "failed" ? "alert" : "status"}>
          {status}
        </span>
        {!compact && quality && (
          <span className="ml-auto tabular-nums">
            {quality.width} × {quality.height} · {quality.fps} FPS
          </span>
        )}
      </div>
      {!compact && (
        <MirrorControlBar
          scope={scope}
          runtime={runtime}
          source={binding}
          state={state.data?.state ?? null}
          videoReady={video.state === "streaming"}
          control={video.control}
          onCommand={runCommand}
          onStartControl={() => void video.startControl()}
          onStopControl={() => void video.stopControl()}
          onType={sendType}
          onKey={sendKey}
          onVoice={(recording) => video.sendVoice(recording)}
          voiceTranscript={video.voiceTranscript}
          agents={(agents.data ?? []).filter((agent) => agent.runtime_id === runtime.id && !agent.archived_at).map((agent) => ({ id: agent.id, name: agent.name }))}
          onAgentMessage={sendAgentMessage}
        />
      )}
      {!compact && (
        <MirrorCommandControls
          access={access}
          state={{
            pending: commandPending,
            uncertain: commandUncertain,
            attempted: commandId !== null || command.isError,
            succeeded: receipt.data?.state === "succeeded",
          }}
          onCommand={runCommand}
          onRefresh={() => void receipt.refetch()}
        />
      )}
      <MirrorHandoff permissions={state.data?.state.permissions} canRequest={access.canRequestTakeover && !commandPending && !commandUncertain} scope={scope} owner={runtime.owner_id === scope.accountId} online={online} catalog={catalog} onRequest={() => runCommand("request_takeover")} />
    </section>
  );
}
