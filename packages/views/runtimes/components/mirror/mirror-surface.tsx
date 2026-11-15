"use client";

import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, type WheelEvent as ReactWheelEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Bot,
  Copy,
  Gauge,
  Keyboard,
  Loader2,
  LogOut,
  Maximize2,
  Minimize2,
  Monitor,
  MousePointer2,
  PanelRight,
  PictureInPicture2,
  RefreshCw,
  Send,
  Sparkles,
  Square,
  UserRound,
  Wifi,
  X,
  ZoomIn,
  ZoomOut,
} from "@multica/ui/icons";
import type { RuntimeDevice, VscreenScope } from "@multica/core/types";
import { defaultStorage } from "@multica/core/platform";
import {
  agentDesktopReadinessOptions,
  runtimeDisplayLabel,
  vscreenSourcesOptions,
} from "@multica/core/runtimes";
import { getApi, vscreenErrorReason } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../../i18n";
import { agentListOptions } from "@multica/core/workspace/queries";
import { useVideoSession } from "./use-video-session";
import type { MirrorVideoState } from "./video-session";
import { MirrorVideo } from "./mirror-video";
import { useMirrorPlatform } from "./mirror-platform";
import { MirrorSourcePicker, mirrorSourceKey } from "./mirror-source-picker";
import {
  parseMirrorQualityMode,
  selectAdaptiveVideoProfile,
  type MirrorQualityMode,
} from "./mirror-quality";
import { useMirrorControl } from "./use-mirror-control";
import { remoteKeyCombo } from "./mirror-keyboard";
import type { MirrorVideoPoint } from "./mirror-video";
import { assertNever } from "./assert-never";

type MirrorViewMode = "fit" | "actual" | "fill";
type MirrorInspectorTab = "session" | "display" | "control" | "ai" | "takeover" | "diagnostics";

export function mirrorConnectionStage(input: {
  readonly readable: boolean;
  readonly online: boolean;
  readonly sourcesPending: boolean;
  readonly hasSource: boolean;
  readonly videoState: MirrorVideoState;
  readonly reason: string | null;
}): number {
  const { readable, online, sourcesPending, hasSource, videoState, reason } = input;
  if (!readable) return 0;
  if (!online || sourcesPending) return 1;
  if (!hasSource) return 2;
  if (videoState === "failed") {
    if (reason === "permission_denied" || reason === "permission-denied") return 0;
    if (reason === "transport" || reason === "negotiation_timeout" || reason === "negotiation-failed") return 3;
    return 2;
  }
  if (videoState === "preparing") return 2;
  if (videoState === "negotiating") return 3;
  if (videoState === "decoding") return 4;
  if (videoState === "streaming") return 5;
  return 2;
}

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
  const { role } = useCurrentMember(scope.workspaceId);
  const platform = useMirrorPlatform();
  const readable =
    !!runtime.owner_id &&
    (runtime.owner_id === scope.accountId || runtime.visibility === "public");
  const online = runtime.status === "online" && !!runtime.daemon_id;
  const sources = useQuery({
    ...vscreenSourcesOptions(scope),
    enabled: online && readable,
  });
  const readiness = useQuery({
    ...agentDesktopReadinessOptions(scope),
    enabled: online && readable,
  });
  const [selected, setSelected] = useState("");
  const retry = 0;
  const [floatingError, setFloatingError] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [inspectorTab, setInspectorTab] = useState<MirrorInspectorTab>("session");
  const [inspectorOpen, setInspectorOpen] = useState(() =>
    typeof window === "undefined"
      ? !compact
      : !compact && (window.matchMedia?.("(min-width: 1280px)").matches ?? false),
  );
  const [viewMode, setViewMode] = useState<MirrorViewMode>(() =>
    typeof window !== "undefined" &&
    window.matchMedia?.("(min-width: 1280px)").matches
      ? "fill"
      : "fit",
  );
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const qualityStorageKey = `mirror-quality:${scope.accountId}:${scope.runtimeId}`;
  const [qualityMode, setQualityMode] = useState<MirrorQualityMode>(() =>
    parseMirrorQualityMode(defaultStorage.getItem(qualityStorageKey)),
  );
  const [adaptiveProfile, setAdaptiveProfile] = useState<"smooth" | "clear" | "saver">("smooth");
  const control = useMirrorControl(scope, online && readable);
  const agents = useQuery({
    ...agentListOptions(scope.workspaceId),
    enabled: online && readable,
  });
  const [captureKeyboard, setCaptureKeyboard] = useState(false);
  const [pointerMode, setPointerMode] = useState<"absolute" | "relative">("absolute");
  const [textInput, setTextInput] = useState("");
  const [clipboardText, setClipboardText] = useState("");
  const [clipboardStatus, setClipboardStatus] = useState<"idle" | "pushing" | "pulling" | "done" | "error">("idle");
  const [selectedAgent, setSelectedAgent] = useState("");
  const [instruction, setInstruction] = useState("");
  const [instructionStatus, setInstructionStatus] = useState<"idle" | "sending" | "sent" | "error">("idle");
  const gestureHintKey = `mirror-gesture-hint:${scope.accountId}`;
  const [gestureHintVisible, setGestureHintVisible] = useState(
    () => defaultStorage.getItem(gestureHintKey) !== "dismissed",
  );
  const [chromeVisible, setChromeVisible] = useState(true);
  const [now, setNow] = useState(() => Date.now());
  const surfaceRef = useRef<HTMLElement>(null);
  const chromeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const drag = useRef<{ x: number; y: number; panX: number; panY: number } | null>(null);
  const remoteGesture = useRef<{
    readonly pointerId: number;
    readonly start: MirrorVideoPoint;
    last: MirrorVideoPoint;
    moved: boolean;
    sentDown: boolean;
    longPress: boolean;
  } | null>(null);
  const touches = useRef(new Map<number, MirrorVideoPoint>());
  const pinch = useRef<{
    readonly distance: number;
    readonly zoom: number;
    readonly center: MirrorVideoPoint;
    readonly pan: { readonly x: number; readonly y: number };
  } | null>(null);
  const lastRemotePoint = useRef<MirrorVideoPoint | null>(null);
  const relativePending = useRef({ x: 0, y: 0 });
  const lastRelativeMoveAt = useRef(0);
  const touchScrollPending = useRef({ x: 0, y: 0 });
  const lastTouchScrollAt = useRef(0);
  const touchLongPressTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const remoteTextId = useId();
  const selectedOnce = useRef(false);
  const catalog = useMemo(
    () => sources.isError ? [] : (sources.data?.sources ?? []),
    [sources.data?.sources, sources.isError],
  );
  const source = catalog.find(
    (binding) => mirrorSourceKey(binding) === selected,
  );
  const binding = online && readable && source ? source : null;
  const effectiveVideoProfile = qualityMode === "auto" ? adaptiveProfile : qualityMode;
  const video = useVideoSession(scope, binding, retry, effectiveVideoProfile);

  useEffect(() => {
    setQualityMode(parseMirrorQualityMode(defaultStorage.getItem(qualityStorageKey)));
    setAdaptiveProfile("smooth");
  }, [qualityStorageKey]);

  useEffect(
    () => () => {
      if (touchLongPressTimer.current) clearTimeout(touchLongPressTimer.current);
    },
    [],
  );

  useEffect(() => {
    const targetQuality = video.quality ?? video.metadata?.quality;
    if (qualityMode !== "auto" || !video.statistics || !targetQuality) return;
    const next = selectAdaptiveVideoProfile({
      current: adaptiveProfile,
      roundTripTimeMs: video.statistics.roundTripTimeMs,
      packetLossPercent: video.statistics.packetLossPercent,
      framesPerSecond: video.statistics.framesPerSecond,
      targetFramesPerSecond: targetQuality.fps,
    });
    if (next === "auto" || next === adaptiveProfile) return;
    setAdaptiveProfile(next);
  }, [adaptiveProfile, qualityMode, video.metadata?.quality, video.quality, video.statistics]);

  useEffect(() => {
    if (video.retryAt === null) return;
    const timer = window.setInterval(() => setNow(Date.now()), 250);
    return () => window.clearInterval(timer);
  }, [video.retryAt]);

  useEffect(() => {
    if (!fullscreen) {
      setChromeVisible(true);
      return;
    }
    const reveal = () => {
      setChromeVisible(true);
      if (chromeTimer.current) clearTimeout(chromeTimer.current);
      chromeTimer.current = setTimeout(() => setChromeVisible(false), 2_500);
    };
    reveal();
    window.addEventListener("pointermove", reveal);
    return () => {
      window.removeEventListener("pointermove", reveal);
      if (chromeTimer.current) clearTimeout(chromeTimer.current);
      chromeTimer.current = null;
    };
  }, [fullscreen]);

  useEffect(() => {
    if (selectedOnce.current || !sources.isSuccess || catalog.length === 0) {
      return;
    }
    selectedOnce.current = true;
    const primary = catalog.find((entry) => entry.primary) ?? catalog[0];
    if (primary) setSelected(mirrorSourceKey(primary));
  }, [catalog, sources.isSuccess]);

  const eligibleAgents = useMemo(
    () => (agents.data ?? []).filter(
      (agent) => !agent.archived_at && agent.runtime_id === runtime.id && agent.runtime_bound !== false,
    ),
    [agents.data, runtime.id],
  );
  useEffect(() => {
    if (selectedAgent && eligibleAgents.some((agent) => agent.id === selectedAgent)) return;
    setSelectedAgent(eligibleAgents[0]?.id ?? "");
  }, [eligibleAgents, selectedAgent]);

  useEffect(() => {
    const onFullscreenChange = () =>
      setFullscreen(document.fullscreenElement === surfaceRef.current);
    document.addEventListener("fullscreenchange", onFullscreenChange);
    return () =>
      document.removeEventListener("fullscreenchange", onFullscreenChange);
  }, []);

  const toggleFullscreen = async () => {
    const surface = surfaceRef.current;
    if (!surface || typeof document === "undefined") return;
    try {
      if (document.fullscreenElement === surface) {
        await document.exitFullscreen();
      } else if (surface.requestFullscreen) {
        await surface.requestFullscreen();
      }
    } catch (error) {
      if (error instanceof TypeError) setFullscreen(false);
      else throw error;
    }
  };

  const reason =
    video.reason ?? vscreenErrorReason(sources.error);
  const status = !readable
    ? t(($) => $.vscreen.access_denied)
    : !online || reason === "daemon_unavailable" || reason === "offline"
      ? t(($) => $.vscreen.offline)
      : reason === "permission_denied" ||
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
                : video.reconnecting
                  ? t(($) => $.vscreen.reconnecting)
                  : sources.isError || video.state === "failed"
                    ? t(($) => $.vscreen.failed)
                    : catalog.length === 0
                      ? t(($) => $.vscreen.empty)
                      : !selected
                        ? t(($) => $.vscreen.choose_source)
                        : video.state === "streaming"
                          ? t(($) => $.vscreen.streaming)
                          : t(($) => $.vscreen.negotiating);
  const label = runtimeDisplayLabel(runtime);
  const quality = video.quality ?? video.metadata?.quality;
  const statistics = video.statistics;
  const measuredStats = quality && [
    `${Math.round(statistics?.framesPerSecond ?? quality.fps)} FPS`,
    statistics?.bitsPerSecond === null ||
    statistics?.bitsPerSecond === undefined
      ? null
      : `${(statistics.bitsPerSecond / 1_000_000).toFixed(1)} Mbps`,
    statistics?.roundTripTimeMs === null ||
    statistics?.roundTripTimeMs === undefined
      ? null
      : `${Math.round(statistics.roundTripTimeMs)} ms RTT`,
    statistics?.packetLossPercent === null ||
    statistics?.packetLossPercent === undefined
      ? null
      : `${statistics.packetLossPercent.toFixed(1)}% loss`,
  ]
    .filter((value): value is string => value !== null)
    .join(" · ");
  const networkHealth =
    statistics?.roundTripTimeMs === undefined ||
    statistics.roundTripTimeMs === null
      ? "unknown"
      : statistics.roundTripTimeMs <= 80 &&
          (statistics.packetLossPercent ?? 0) < 1
        ? "good"
        : statistics.roundTripTimeMs <= 180 &&
            (statistics.packetLossPercent ?? 0) < 3
          ? "fair"
          : "poor";
  const retrySeconds = video.retryAt === null
    ? null
    : Math.max(0, Math.ceil((video.retryAt - now) / 1_000));
  const selectedIndex = catalog.findIndex(
    (entry) => mirrorSourceKey(entry) === selected,
  );
  const sourceLabel = source?.name.trim() || (selectedIndex >= 0 ? String(selectedIndex + 1) : "—");
  const connectionStage = mirrorConnectionStage({
    readable,
    online,
    sourcesPending: sources.isPending,
    hasSource: !!source,
    videoState: video.state,
    reason,
  });
  const resetView = () => {
    setViewMode("fit");
    setZoom(1);
    setPan({ x: 0, y: 0 });
  };
  const adjustZoom = (next: number) => {
    setViewMode("actual");
    setZoom(Math.min(4, Math.max(0.25, next)));
  };
  const startPan = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (controlling) return;
    if (zoom <= 1 && viewMode !== "actual") return;
    event.currentTarget.setPointerCapture(event.pointerId);
    drag.current = { x: event.clientX, y: event.clientY, panX: pan.x, panY: pan.y };
  };
  const movePan = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!drag.current) return;
    setPan({
      x: drag.current.panX + event.clientX - drag.current.x,
      y: drag.current.panY + event.clientY - drag.current.y,
    });
  };
  const endPan = () => {
    drag.current = null;
  };
  const updateQualityMode = (next: MirrorQualityMode) => {
    if (next === "auto" && qualityMode !== "auto") {
      setAdaptiveProfile(qualityMode);
    }
    setQualityMode(next);
    defaultStorage.setItem(qualityStorageKey, next);
  };
  const controlState = control.status?.state ?? "view_only";
  const canManageControl =
    runtime.owner_id === scope.accountId ||
    role === "owner" ||
    role === "admin";
  const controlling =
    controlState === "controlling" &&
    video.state === "streaming" &&
    !!binding;
  const stopControl = control.stop;
  useEffect(() => {
    if (!controlling) {
      setCaptureKeyboard(false);
      return;
    }
    const releaseOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      event.preventDefault();
      setCaptureKeyboard(false);
      void stopControl();
    };
    window.addEventListener("keydown", releaseOnEscape);
    return () => window.removeEventListener("keydown", releaseOnEscape);
  }, [controlling, stopControl]);
  const controlLabel = (() => {
    switch (controlState) {
      case "view_only":
        return t(($) => $.vscreen.view_only);
      case "waiting_for_approval":
        return t(($) => $.vscreen.waiting_for_approval);
      case "approval_pending":
        return t(($) => $.vscreen.approval_pending);
      case "controlling":
        return t(($) => $.vscreen.you_are_controlling);
      case "other_controller":
        return t(($) => $.vscreen.other_controller);
      case "ended":
        return t(($) => $.vscreen.control_ended);
      default:
        return assertNever(controlState);
    }
  })();
  const controlBadgeLabel =
    controlState === "other_controller" && control.status?.controllerId
      ? t(($) => $.vscreen.controller_source, {
          id: control.status?.controllerId.slice(0, 12),
          source: sourceLabel,
        })
      : controlLabel;
  const dismissGestureHint = () => {
    setGestureHintVisible(false);
    defaultStorage.setItem(gestureHintKey, "dismissed");
  };
  const remoteSource = binding?.source;
  const sendRemoteInput = async (
    input: Omit<import("@multica/core/types").AgentDesktopControlInput, "source">,
  ) => {
    if (!remoteSource || !controlling) return null;
    return control.send({ source: remoteSource, ...input });
  };
  const clearTouchLongPress = () => {
    if (touchLongPressTimer.current) clearTimeout(touchLongPressTimer.current);
    touchLongPressTimer.current = null;
  };
  const flushTouchScroll = () => {
    const pending = touchScrollPending.current;
    touchScrollPending.current = { x: 0, y: 0 };
    if (!pending.x && !pending.y) return;
    void sendRemoteInput({
      action: "scroll",
      x: lastRemotePoint.current?.x ?? 0,
      y: lastRemotePoint.current?.y ?? 0,
      dx: pending.x,
      dy: pending.y,
    });
  };
  const releaseHeldPointer = () => {
    const point = lastRemotePoint.current;
    if (!point) return;
    void sendRemoteInput({
      action: "mouse_up",
      x: point.x,
      y: point.y,
      button: "left",
    });
  };
  const remotePointerDown = (point: MirrorVideoPoint, event: ReactPointerEvent<HTMLVideoElement>) => {
    if (!controlling) return;
    event.preventDefault();
    event.stopPropagation();
    event.currentTarget.focus();
    if (event.pointerType === "touch") {
      touches.current.set(event.pointerId, { x: point.x, y: point.y });
      if (touches.current.size >= 2) {
        clearTouchLongPress();
        remoteGesture.current = null;
        touchScrollPending.current = { x: 0, y: 0 };
        const [first, second] = [...touches.current.values()];
        if (first && second) {
          pinch.current = {
            distance: Math.hypot(second.x - first.x, second.y - first.y),
            zoom,
            center: { x: (first.x + second.x) / 2, y: (first.y + second.y) / 2 },
            pan,
          };
        }
        return;
      }
    }
    event.currentTarget.setPointerCapture(event.pointerId);
    remoteGesture.current = {
      pointerId: event.pointerId,
      start: point,
      last: point,
      moved: false,
      sentDown: pointerMode === "relative",
      longPress: false,
    };
    relativePending.current = { x: 0, y: 0 };
    lastRelativeMoveAt.current = 0;
    touchScrollPending.current = { x: 0, y: 0 };
    lastTouchScrollAt.current = 0;
    lastRemotePoint.current = point;
    if (event.pointerType === "touch") {
      clearTouchLongPress();
      const pointerId = event.pointerId;
      touchLongPressTimer.current = setTimeout(() => {
        touchLongPressTimer.current = null;
        const gesture = remoteGesture.current;
        if (gesture?.pointerId === pointerId && !gesture.moved) {
          gesture.longPress = true;
        }
      }, 400);
    }
    if (pointerMode === "relative") {
      void sendRemoteInput({ action: "mouse_down", x: point.x, y: point.y, button: "left" });
    }
  };
  const remotePointerMove = (point: MirrorVideoPoint, event: ReactPointerEvent<HTMLVideoElement>) => {
    if (!controlling) return;
    event.preventDefault();
    event.stopPropagation();
    if (event.pointerType === "touch" && touches.current.has(event.pointerId)) {
      touches.current.set(event.pointerId, { x: point.x, y: point.y });
      if (touches.current.size >= 2 && pinch.current) {
        const [first, second] = [...touches.current.values()];
        if (first && second) {
          const distance = Math.hypot(second.x - first.x, second.y - first.y);
          const center = {
            x: (first.x + second.x) / 2,
            y: (first.y + second.y) / 2,
          };
          setZoom(Math.min(4, Math.max(0.25, pinch.current.zoom * (distance / Math.max(0.001, pinch.current.distance)))));
          setPan({
            x: pinch.current.pan.x + (center.x - pinch.current.center.x) * 1200,
            y: pinch.current.pan.y + (center.y - pinch.current.center.y) * 800,
          });
        }
        return;
      }
    }
    const gesture = remoteGesture.current;
    if (!gesture || gesture.pointerId !== event.pointerId) return;
    const dx = point.x - gesture.last.x;
    const dy = point.y - gesture.last.y;
    const moved =
      Math.abs(point.x - gesture.start.x) > 0.006 ||
      Math.abs(point.y - gesture.start.y) > 0.006;
    if (moved && !gesture.longPress) clearTouchLongPress();
    if (moved) {
      gesture.moved = true;
    }
    gesture.last = point;
    lastRemotePoint.current = point;
    if (event.pointerType === "touch" && !gesture.longPress) {
      touchScrollPending.current = {
        x: touchScrollPending.current.x - dx * 12,
        y: touchScrollPending.current.y - dy * 12,
      };
      const currentTime = Date.now();
      if (currentTime - lastTouchScrollAt.current >= 45) {
        lastTouchScrollAt.current = currentTime;
        flushTouchScroll();
      }
    } else if (pointerMode === "relative" && gesture.sentDown && (Math.abs(dx) > 0.001 || Math.abs(dy) > 0.001)) {
      relativePending.current = {
        x: relativePending.current.x + dx,
        y: relativePending.current.y + dy,
      };
      const currentTime = Date.now();
      if (currentTime - lastRelativeMoveAt.current >= 45) {
        lastRelativeMoveAt.current = currentTime;
        const pending = relativePending.current;
        relativePending.current = { x: 0, y: 0 };
        void sendRemoteInput({ action: "move_relative", x: pending.x, y: pending.y });
      }
    }
  };
  const remotePointerUp = (point: MirrorVideoPoint, event: ReactPointerEvent<HTMLVideoElement>) => {
    clearTouchLongPress();
    touches.current.delete(event.pointerId);
    if (touches.current.size < 2) pinch.current = null;
    if (!controlling) return;
    event.preventDefault();
    event.stopPropagation();
    const gesture = remoteGesture.current;
    if (!gesture || gesture.pointerId !== event.pointerId) return;
    remoteGesture.current = null;
    lastRemotePoint.current = point;
    if (event.pointerType === "touch" && !gesture.longPress) {
      flushTouchScroll();
      if (gesture.moved) return;
    }
    if (pointerMode === "relative" && gesture.sentDown) {
      void (async () => {
        const pending = relativePending.current;
        relativePending.current = { x: 0, y: 0 };
        if (pending.x || pending.y) {
          await sendRemoteInput({ action: "move_relative", x: pending.x, y: pending.y });
        }
        await sendRemoteInput({ action: "mouse_up", x: point.x, y: point.y, button: "left" });
      })();
    } else if (gesture.moved) {
      void sendRemoteInput({
        action: "drag",
        x: gesture.start.x,
        y: gesture.start.y,
        x2: point.x,
        y2: point.y,
        button: "left",
      });
    } else {
      void sendRemoteInput({
        action: event.button === 2 ? "right_click" : "click",
        x: point.x,
        y: point.y,
        button: event.button === 2 ? "right" : "left",
      });
    }
  };
  const remotePointerCancel = (event: ReactPointerEvent<HTMLVideoElement>) => {
    clearTouchLongPress();
    touches.current.delete(event.pointerId);
    pinch.current = null;
    const gesture = remoteGesture.current;
    remoteGesture.current = null;
    touchScrollPending.current = { x: 0, y: 0 };
    relativePending.current = { x: 0, y: 0 };
    if (gesture?.sentDown && lastRemotePoint.current) {
      releaseHeldPointer();
    }
  };
  const remoteWheel = (point: MirrorVideoPoint, event: ReactWheelEvent<HTMLVideoElement>) => {
    if (!controlling) return;
    event.preventDefault();
    event.stopPropagation();
    void sendRemoteInput({
      action: "scroll",
      x: point.x,
      y: point.y,
      dx: Math.round(event.deltaX / 80),
      dy: -Math.round(event.deltaY / 80),
    });
  };
  const remoteKeyDown = (event: ReactKeyboardEvent<HTMLVideoElement>) => {
    if (!captureKeyboard || !controlling) return;
    const combo = remoteKeyCombo(event.nativeEvent);
    if (!combo) return;
    event.preventDefault();
    event.stopPropagation();
    void sendRemoteInput({ action: "press", key: combo });
  };
  const sendRemoteText = async () => {
    const text = textInput;
    if (!text.trim() || !controlling) return;
    const sent = await sendRemoteInput({ action: "text", text });
    if (sent) setTextInput("");
  };
  const pushBrowserClipboard = async () => {
    if (!controlling) return;
    setClipboardStatus("pushing");
    try {
      const text = await navigator.clipboard.readText();
      const sent = await sendRemoteInput({ action: "clipboard_set", text });
      setClipboardStatus(sent ? "done" : "error");
    } catch (reason) {
      if (reason instanceof Error) setClipboardStatus("error");
      else throw reason;
    }
  };
  const pullRemoteClipboard = async () => {
    if (!controlling) return;
    setClipboardStatus("pulling");
    try {
      const result = await sendRemoteInput({ action: "clipboard_get" });
      if (!result || result.clipboard === null) {
        setClipboardStatus("error");
        return;
      }
      setClipboardText(result.clipboard);
      await navigator.clipboard.writeText(result.clipboard);
      setClipboardStatus("done");
    } catch (reason) {
      if (reason instanceof Error) setClipboardStatus("error");
      else throw reason;
    }
  };
  const sendAgentInstruction = async () => {
    if (!binding || !selectedAgent || !instruction.trim()) return;
    setInstructionStatus("sending");
    try {
      const session = await getApi().createChatSession({
        agent_id: selectedAgent,
        title: `${sourceLabel} · ${label}`,
      });
      await getApi().sendChatMessage(session.id, instruction.trim(), undefined, binding);
      setInstructionStatus("sent");
      setInstruction("");
    } catch (reason) {
      if (reason instanceof Error) setInstructionStatus("error");
      else throw reason;
    }
  };
  const inspectorTabs = [
    ["session", t(($) => $.vscreen.tab_session), Wifi],
    ["display", t(($) => $.vscreen.tab_display), Monitor],
    ["control", t(($) => $.vscreen.tab_control), MousePointer2],
    ["ai", t(($) => $.vscreen.tab_ai), Sparkles],
    ["takeover", t(($) => $.vscreen.tab_takeover), UserRound],
    ["diagnostics", t(($) => $.vscreen.tab_diagnostics), Gauge],
  ] as const;
  const handleInspectorTabKeyDown = (
    event: ReactKeyboardEvent<HTMLButtonElement>,
    currentIndex: number,
  ) => {
    const nextIndex = event.key === "Home"
      ? 0
      : event.key === "End"
        ? inspectorTabs.length - 1
        : event.key === "ArrowRight"
          ? (currentIndex + 1) % inspectorTabs.length
          : event.key === "ArrowLeft"
            ? (currentIndex - 1 + inspectorTabs.length) % inspectorTabs.length
            : -1;
    if (nextIndex < 0) return;
    event.preventDefault();
    setInspectorTab(inspectorTabs[nextIndex]?.[0] ?? "session");
    event.currentTarget.parentElement
      ?.querySelectorAll<HTMLButtonElement>('[role="tab"]')
      .item(nextIndex)
      .focus();
  };

  const stages = [
    t(($) => $.vscreen.stage_permission),
    t(($) => $.vscreen.stage_daemon),
    t(($) => $.vscreen.stage_session),
    t(($) => $.vscreen.stage_ice),
    t(($) => $.vscreen.stage_decode),
    t(($) => $.vscreen.stage_streaming),
  ] as const;
  const inspector = (
    <aside
      className={`${compact ? "absolute inset-x-0 bottom-0 z-30 h-[min(76dvh,36rem)] w-full rounded-t-2xl border-t pb-[env(safe-area-inset-bottom)] shadow-xl" : "absolute inset-x-0 bottom-0 z-30 h-[min(72dvh,34rem)] rounded-t-2xl border-t pb-[env(safe-area-inset-bottom)] shadow-2xl lg:static lg:h-auto lg:rounded-none lg:border-l lg:border-t-0 lg:pb-0 lg:shadow-none"} flex min-h-0 flex-col overflow-hidden bg-card`}
      aria-label={t(($) => $.vscreen.inspector)}
    >
      <div className="mx-auto mt-1 h-1 w-10 shrink-0 rounded-full bg-border lg:hidden" aria-hidden="true" />
      <div className="grid shrink-0 grid-cols-[repeat(6,minmax(0,1fr))_auto] border-b p-1" role="tablist">
        {inspectorTabs.map(([id, text, Icon], index) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={inspectorTab === id}
            aria-controls="mirror-inspector-panel"
            tabIndex={inspectorTab === id ? 0 : -1}
            title={text}
            className={`flex flex-col items-center gap-1 rounded-md px-1 py-1.5 text-micro ${inspectorTab === id ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-accent/60"}`}
            onClick={() => setInspectorTab(id)}
            onKeyDown={(event) => handleInspectorTabKeyDown(event, index)}
          >
            <Icon className="size-3.5" aria-hidden="true" />
            <span className="w-full truncate">{text}</span>
          </button>
        ))}
        <button
          type="button"
          className={`${compact ? "" : "lg:hidden"} flex items-center justify-center rounded-md px-2 text-muted-foreground hover:bg-accent`}
          aria-label={t(($) => $.vscreen.close)}
          onClick={() => setInspectorOpen(false)}
        >
          <X className="size-4" aria-hidden="true" />
        </button>
      </div>
      <div id="mirror-inspector-panel" role="tabpanel" className="min-h-0 flex-1 overflow-y-auto p-3 text-caption">
        {inspectorTab === "session" && (
          <div className="space-y-4">
            <section>
              <h3 className="font-semibold">{t(($) => $.vscreen.connection_status)}</h3>
              <ol className="mt-2 space-y-2">
                {stages.map((stage, index) => {
                  const done = index < connectionStage;
                  const current = index === connectionStage;
                  return (
                    <li key={stage} className="flex items-center gap-2">
                      <span className={`flex size-4 shrink-0 items-center justify-center rounded-full border text-micro ${done ? "border-success bg-success text-success-foreground" : current ? "border-primary text-primary" : "text-muted-foreground"}`}>
                        {done ? "✓" : index + 1}
                      </span>
                      <span className={current ? "font-medium text-foreground" : "text-muted-foreground"}>{stage}</span>
                    </li>
                  );
                })}
              </ol>
            </section>
            {readiness.data && (
              <section>
                <h3 className="font-semibold">{t(($) => $.vscreen.permissions)}</h3>
                <dl className="mt-2 space-y-1">
                  <div className="flex justify-between gap-2"><dt>{t(($) => $.vscreen.permission_screen_recording)}</dt><dd>{readiness.data.permissions.screenRecording}</dd></div>
                  <div className="flex justify-between gap-2"><dt>{t(($) => $.vscreen.permission_accessibility)}</dt><dd>{readiness.data.permissions.accessibility}</dd></div>
                  <div className="flex justify-between gap-2"><dt>{t(($) => $.vscreen.permission_automation)}</dt><dd>{readiness.data.permissions.automation}</dd></div>
                </dl>
                <Button
                  className="mt-2"
                  size="xs"
                  variant="outline"
                  onClick={async () => {
                    await getApi().vscreen(scope).openAgentDesktopPermissionGuide();
                    await readiness.refetch();
                  }}
                >
                  {t(($) => $.vscreen.open_permission_settings)}
                </Button>
              </section>
            )}
            {video.reconnecting && video.retryAt !== null && (
              <section className="rounded-md border border-warning/40 bg-warning/5 p-2">
                <p>{t(($) => $.vscreen.reconnect_countdown, {
                  count: retrySeconds ?? 0,
                  attempt: video.retryAttempt,
                })}</p>
                <div className="mt-2 flex gap-1">
                  <Button size="xs" onClick={() => { void sources.refetch(); video.retryNow(); }}>
                    {t(($) => $.vscreen.retry_now)}
                  </Button>
                  <Button size="xs" variant="ghost" onClick={video.cancelAutoRetry}>
                    {t(($) => $.vscreen.cancel_reconnect)}
                  </Button>
                </div>
              </section>
            )}
            {status !== t(($) => $.vscreen.streaming) && (
              <section className="rounded-md border bg-muted/20 p-2">
                <p className="font-medium">{t(($) => $.vscreen.next_step)}</p>
                <p className="mt-1 text-muted-foreground">{status}</p>
                <Button className="mt-2" size="xs" variant="outline" onClick={() => { void sources.refetch(); video.retryNow(); }}>
                  {t(($) => $.vscreen.retry)}
                </Button>
              </section>
            )}
          </div>
        )}
        {inspectorTab === "display" && (
          <div className="space-y-4">
            <section>
              <h3 className="font-semibold">{t(($) => $.vscreen.current_source)}</h3>
              <p className="mt-1 break-words">{sourceLabel}</p>
              <p className="text-muted-foreground">
                {source ? `${source.width} × ${source.height} · ${Math.round(source.scale * 100)}%` : t(($) => $.vscreen.choose_source)}
              </p>
            </section>
            <section>
              <h3 className="mb-2 font-semibold">{t(($) => $.vscreen.view)}</h3>
              <div className="grid grid-cols-3 gap-1">
                {([
                  ["fit", t(($) => $.vscreen.view_fit)],
                  ["actual", t(($) => $.vscreen.view_actual)],
                  ["fill", t(($) => $.vscreen.view_fill)],
                ] as const).map(([mode, text]) => (
                  <Button key={mode} size="xs" variant={viewMode === mode ? "secondary" : "outline"} aria-pressed={viewMode === mode} onClick={() => { setViewMode(mode); if (mode === "fit") setZoom(1); setPan({ x: 0, y: 0 }); }}>{text}</Button>
                ))}
              </div>
              <div className="mt-2 flex items-center gap-1">
                <Button size="icon-xs" variant="ghost" aria-label={t(($) => $.vscreen.zoom_out)} onClick={() => adjustZoom(zoom - 0.25)}><ZoomOut aria-hidden="true" /></Button>
                <span className="min-w-14 text-center tabular-nums">{Math.round(zoom * 100)}%</span>
                <Button size="icon-xs" variant="ghost" aria-label={t(($) => $.vscreen.zoom_in)} onClick={() => adjustZoom(zoom + 0.25)}><ZoomIn aria-hidden="true" /></Button>
                <Button size="xs" variant="ghost" onClick={resetView}>{t(($) => $.vscreen.reset_view)}</Button>
              </div>
            </section>
            <section>
              <label className="block font-semibold" htmlFor="mirror-quality">{t(($) => $.vscreen.quality)}</label>
              <select id="mirror-quality" value={qualityMode} onChange={(event) => updateQualityMode(event.target.value as MirrorQualityMode)} className="mt-1 h-8 w-full rounded-md border bg-background px-2">
                <option value="auto">{t(($) => $.vscreen.quality_auto)}</option>
                <option value="clear">{t(($) => $.vscreen.quality_clear)}</option>
                <option value="smooth">{t(($) => $.vscreen.quality_smooth)}</option>
                <option value="saver">{t(($) => $.vscreen.quality_saver)}</option>
              </select>
              <p className="mt-1 text-muted-foreground">{t(($) => $.vscreen.quality_hint)}</p>
            </section>
          </div>
        )}
        {inspectorTab === "control" && (
          <div className="space-y-4">
            <section className="rounded-md border p-2">
              <div className="flex items-center gap-2">
                <Bot className="size-4 text-muted-foreground" aria-hidden="true" />
                <h3 className="font-semibold">{controlLabel}</h3>
              </div>
              <p className="mt-1 text-muted-foreground">
                {controlState === "controlling"
                  ? control.leaseSeconds === null
                    ? t(($) => $.vscreen.control_active)
                    : t(($) => $.vscreen.control_lease_remaining, { count: control.leaseSeconds })
                  : controlState === "waiting_for_approval"
                    ? t(($) => $.vscreen.control_waiting_hint)
                    : controlState === "approval_pending"
                      ? t(($) => $.vscreen.control_approval_hint)
                      : controlState === "other_controller"
                        ? t(($) => $.vscreen.control_busy_hint)
                        : controlState === "ended"
                          ? t(($) => $.vscreen.control_ended_hint)
                          : t(($) => $.vscreen.view_only_description)}
              </p>
              {(controlState === "view_only" || controlState === "ended") && (
                <Button
                  className="mt-2"
                  size="sm"
                  variant="outline"
                  disabled={control.pending || !online || !readable}
                  onClick={() => void control.request()}
                >
                  {control.pending && <Loader2 className="animate-spin" aria-hidden="true" />}
                  {t(($) => $.vscreen.request_control)}
                </Button>
              )}
              {controlState === "waiting_for_approval" && (
                <Button className="mt-2" size="sm" variant="ghost" disabled={control.pending} onClick={() => void control.stop()}>
                  {t(($) => $.vscreen.cancel_control_request)}
                </Button>
              )}
              {controlState === "approval_pending" && control.status?.requestId && (
                <div className="mt-2 flex flex-wrap gap-1">
                  <Button size="sm" disabled={control.pending} onClick={() => void control.approve(control.status?.requestId ?? "")}>
                    {t(($) => $.vscreen.approve_control)}
                  </Button>
                  <Button size="sm" variant="ghost" disabled={control.pending} onClick={() => void control.deny(control.status?.requestId ?? "")}>
                    {t(($) => $.vscreen.deny_control)}
                  </Button>
                </div>
              )}
              {controlState === "other_controller" && control.status?.controllerId && (
                <div className="mt-2 space-y-2">
                  <p className="break-all font-mono text-muted-foreground">
                    {controlBadgeLabel}
                  </p>
                  {canManageControl && (
                    <div>
                      <Button
                        size="sm"
                        variant="destructive"
                        disabled={control.pending}
                        onClick={() => void control.stop()}
                      >
                        <Square aria-hidden="true" />
                        {t(($) => $.vscreen.take_back_control)}
                      </Button>
                      <p className="mt-1 text-muted-foreground">
                        {t(($) => $.vscreen.take_back_control_hint)}
                      </p>
                    </div>
                  )}
                </div>
              )}
              {control.error && (
                <p role="alert" className="mt-2 text-destructive">{t(($) => $.vscreen.control_failed)}</p>
              )}
            </section>
            <section className={`space-y-2 ${controlling ? "" : "opacity-60"}`}>
              <label className="flex items-start gap-2">
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={captureKeyboard}
                  disabled={!controlling}
                  onChange={(event) => setCaptureKeyboard(event.target.checked)}
                />
                <span><span className="flex items-center gap-1 font-medium"><Keyboard className="size-3.5" />{t(($) => $.vscreen.keyboard_capture)}</span><span className="block text-muted-foreground">{t(($) => $.vscreen.keyboard_capture_hint)}</span></span>
              </label>
              <div className="space-y-1">
                <span className="flex items-center gap-1 font-medium"><Copy className="size-3.5" />{t(($) => $.vscreen.clipboard_sync)}</span>
                <p className="text-muted-foreground">{t(($) => $.vscreen.clipboard_sync_hint)}</p>
                <div className="flex flex-wrap gap-1">
                  <Button size="xs" variant="outline" disabled={!controlling || clipboardStatus === "pushing"} onClick={() => void pushBrowserClipboard()}>
                    {t(($) => $.vscreen.clipboard_browser_to_remote)}
                  </Button>
                  <Button size="xs" variant="outline" disabled={!controlling || clipboardStatus === "pulling"} onClick={() => void pullRemoteClipboard()}>
                    {t(($) => $.vscreen.clipboard_remote_to_browser)}
                  </Button>
                </div>
                {clipboardStatus === "done" && <p className="text-success">{t(($) => $.vscreen.clipboard_done)}</p>}
                {clipboardStatus === "error" && <p role="alert" className="text-destructive">{t(($) => $.vscreen.clipboard_failed)}</p>}
                {clipboardText && <p className="max-h-20 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/50 p-2 font-mono">{clipboardText}</p>}
              </div>
              <label className="block">
                <span className="font-medium">{t(($) => $.vscreen.pointer_mode)}</span>
                <select
                  value={pointerMode}
                  disabled={!controlling}
                  onChange={(event) => setPointerMode(event.target.value === "relative" ? "relative" : "absolute")}
                  className="mt-1 h-8 w-full rounded-md border bg-background px-2"
                >
                  <option value="absolute">{t(($) => $.vscreen.pointer_absolute)}</option>
                  <option value="relative">{t(($) => $.vscreen.pointer_relative)}</option>
                </select>
                <span className="mt-1 block text-muted-foreground">{t(($) => $.vscreen.pointer_mode_hint)}</span>
              </label>
            </section>
            <section className="space-y-2 rounded-md border p-2">
              <label htmlFor={remoteTextId} className="font-medium">{t(($) => $.vscreen.text_input)}</label>
              <textarea
                id={remoteTextId}
                value={textInput}
                disabled={!controlling}
                maxLength={10000}
                rows={3}
                onChange={(event) => setTextInput(event.target.value)}
                className="w-full resize-y rounded-md border bg-background px-2 py-1.5"
                placeholder={t(($) => $.vscreen.text_input_placeholder)}
              />
              <Button size="sm" variant="outline" disabled={!controlling || !textInput.trim()} onClick={() => void sendRemoteText()}>
                <Send aria-hidden="true" />
                {t(($) => $.vscreen.send_text)}
              </Button>
            </section>
            <section className="rounded-md border border-destructive/40 bg-destructive/5 p-2">
              <p className="font-medium">{t(($) => $.vscreen.emergency_stop)}</p>
              <p className="mt-1 text-muted-foreground">{t(($) => $.vscreen.emergency_stop_hint)}</p>
              <Button
                className="mt-2"
                size="sm"
                variant="destructive"
                disabled={!controlling || control.pending}
                onClick={() => void control.stop()}
              >
                <Square aria-hidden="true" />
                {t(($) => $.vscreen.emergency_stop)}
              </Button>
            </section>
          </div>
        )}
        {inspectorTab === "ai" && (
          <div className="space-y-3">
            <section>
              <h3 className="font-semibold">{t(($) => $.vscreen.ai_instructions)}</h3>
              <p className="mt-1 text-muted-foreground">{t(($) => $.vscreen.ai_instructions_hint)}</p>
            </section>
            <label className="block">
              <span className="font-medium">{t(($) => $.vscreen.ai_agent)}</span>
              <select
                value={selectedAgent}
                onChange={(event) => setSelectedAgent(event.target.value)}
                className="mt-1 h-8 w-full rounded-md border bg-background px-2"
              >
                {eligibleAgents.length === 0 && <option value="">{t(($) => $.vscreen.no_agents)}</option>}
                {eligibleAgents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
              </select>
            </label>
            <label className="block">
              <span className="font-medium">{t(($) => $.vscreen.ai_message)}</span>
              <textarea
                value={instruction}
                onChange={(event) => setInstruction(event.target.value)}
                rows={5}
                maxLength={8000}
                className="mt-1 w-full resize-y rounded-md border bg-background px-2 py-1.5"
                placeholder={t(($) => $.vscreen.ai_message_placeholder)}
              />
            </label>
            {instructionStatus === "sent" && <p className="text-success">{t(($) => $.vscreen.ai_sent)}</p>}
            {instructionStatus === "error" && <p role="alert" className="text-destructive">{t(($) => $.vscreen.ai_failed)}</p>}
            <Button
              size="sm"
              disabled={!binding || !selectedAgent || !instruction.trim() || instructionStatus === "sending"}
              onClick={() => void sendAgentInstruction()}
            >
              {instructionStatus === "sending" ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Send aria-hidden="true" />}
              {t(($) => $.vscreen.send_instruction)}
            </Button>
          </div>
        )}
        {inspectorTab === "takeover" && (
          <div className="space-y-4">
            <section>
              <h3 className="font-semibold">{t(($) => $.vscreen.takeover)}</h3>
              <p className="mt-1 text-muted-foreground">{t(($) => $.vscreen.takeover_hint)}</p>
            </section>
            <dl className="space-y-2">
              <div className="rounded-md border p-2"><dt className="text-muted-foreground">{t(($) => $.vscreen.control_status)}</dt><dd className="font-medium">{controlLabel}</dd></div>
              {control.status?.leaseExpiresAt && (
                <div className="rounded-md border p-2"><dt className="text-muted-foreground">{t(($) => $.vscreen.lease_expires)}</dt><dd className="break-all font-mono">{control.status.leaseExpiresAt}</dd></div>
              )}
              {control.status?.leaseMaxExpiresAt && (
                <div className="rounded-md border p-2"><dt className="text-muted-foreground">{t(($) => $.vscreen.lease_max_expires)}</dt><dd className="break-all font-mono">{control.status.leaseMaxExpiresAt}</dd></div>
              )}
            </dl>
            {controlling && (
              <Button size="sm" variant="outline" disabled={control.pending} onClick={() => void control.stop()}>
                <LogOut aria-hidden="true" />
                {t(($) => $.vscreen.release_to_agent)}
              </Button>
            )}
            {controlState === "other_controller" && canManageControl && (
              <Button size="sm" variant="destructive" disabled={control.pending} onClick={() => void control.stop()}>
                <Square aria-hidden="true" />
                {t(($) => $.vscreen.take_back_control)}
              </Button>
            )}
          </div>
        )}
        {inspectorTab === "diagnostics" && (
          <div className="space-y-3">
            <section>
              <h3 className="font-semibold">{t(($) => $.vscreen.network_quality)}</h3>
              <p className="mt-1">
                {networkHealth === "good"
                  ? t(($) => $.vscreen.network_good)
                  : networkHealth === "fair"
                    ? t(($) => $.vscreen.network_fair)
                    : networkHealth === "poor"
                      ? t(($) => $.vscreen.network_poor)
                      : t(($) => $.vscreen.measuring)}
              </p>
            </section>
            <dl className="grid grid-cols-2 gap-2">
              <div className="rounded-md border p-2"><dt className="text-muted-foreground">FPS</dt><dd className="text-body font-medium tabular-nums">{Math.round(statistics?.framesPerSecond ?? quality?.fps ?? 0)}</dd></div>
              <div className="rounded-md border p-2"><dt className="text-muted-foreground">{t(($) => $.vscreen.mbps)}</dt><dd className="text-body font-medium tabular-nums">{((statistics?.bitsPerSecond ?? quality?.bitrate ?? 0) / 1_000_000).toFixed(1)}</dd></div>
              <div className="rounded-md border p-2"><dt className="text-muted-foreground">{t(($) => $.vscreen.rtt)}</dt><dd className="text-body font-medium tabular-nums">{t(($) => $.vscreen.milliseconds, { value: Math.round(statistics?.roundTripTimeMs ?? 0) })}</dd></div>
              <div className="rounded-md border p-2"><dt className="text-muted-foreground">{t(($) => $.vscreen.packet_loss)}</dt><dd className="text-body font-medium tabular-nums">{(statistics?.packetLossPercent ?? 0).toFixed(1)}%</dd></div>
            </dl>
            <section>
              <h3 className="font-semibold">{t(($) => $.vscreen.negotiated_quality)}</h3>
              <p className="mt-1 text-muted-foreground">{quality ? `${quality.width} × ${quality.height} · ${quality.fps} FPS · ${(quality.bitrate / 1_000_000).toFixed(1)} Mbps` : t(($) => $.vscreen.measuring)}</p>
            </section>
          </div>
        )}
      </div>
    </aside>
  );

  return (
    <section
      ref={surfaceRef}
      aria-label={t(($) => $.mirror.frame_label)}
      className={`relative ${compact ? "flex h-full min-h-0" : `grid h-full min-h-0 ${inspectorOpen ? "lg:grid-cols-[minmax(0,1fr)_20rem]" : ""}`} overflow-hidden border bg-card ${fullscreen ? "h-dvh min-h-dvh w-dvw rounded-none" : "rounded-lg"}`}
      data-fullscreen={fullscreen ? "true" : "false"}
    >
      <div className="flex min-h-0 min-w-0 flex-col">
        <p className="sr-only" role="status" aria-live="polite">{status}</p>
        <div className={`${fullscreen && !chromeVisible ? "hidden" : "flex"} shrink-0 flex-wrap items-center gap-2 border-b bg-card p-2 [-webkit-app-region:no-drag]`}>
          <span className="flex items-center gap-1.5 rounded-full border bg-muted/30 px-2 py-1 text-caption">
            <span className={`size-1.5 rounded-full ${video.state === "streaming" ? "bg-success" : online ? "bg-warning" : "bg-muted-foreground"}`} aria-hidden="true" />
            <span>{status}</span>
          </span>
          <span className={`rounded-full border px-2 py-1 text-caption ${controlling ? "border-success/40 text-success" : "text-muted-foreground"}`}>{controlBadgeLabel}</span>
          <MirrorSourcePicker
            catalog={catalog}
            selected={selected}
            enabled={online && readable && !sources.isPending}
            compact={compact}
            onSelect={(key) => {
              selectedOnce.current = true;
              setSelected(key);
              resetView();
            }}
          />
          <label className="flex items-center gap-1 text-caption text-muted-foreground">
            <span className="hidden sm:inline">{t(($) => $.vscreen.quality)}</span>
            <select value={qualityMode} onChange={(event) => updateQualityMode(event.target.value as MirrorQualityMode)} aria-label={t(($) => $.vscreen.quality)} className="h-8 rounded-md border bg-background px-2 text-caption text-foreground">
              <option value="auto">{t(($) => $.vscreen.quality_auto)}</option>
              <option value="clear">{t(($) => $.vscreen.quality_clear)}</option>
              <option value="smooth">{t(($) => $.vscreen.quality_smooth)}</option>
              <option value="saver">{t(($) => $.vscreen.quality_saver)}</option>
            </select>
          </label>
          <div className="ml-auto flex items-center gap-1">
            <Button size="icon-sm" variant="ghost" aria-label={t(($) => $.vscreen.retry)} onClick={() => { void sources.refetch(); video.retryNow(); }}>
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
            <Button size="icon-sm" variant={inspectorOpen ? "secondary" : "ghost"} aria-label={t(($) => $.vscreen.inspector)} aria-pressed={inspectorOpen} aria-expanded={inspectorOpen} onClick={() => setInspectorOpen((open) => !open)}>
              <PanelRight aria-hidden="true" />
            </Button>
            <Button size="icon-sm" variant="ghost" aria-label={t(($) => fullscreen ? $.vscreen.exit_fullscreen : $.vscreen.fullscreen)} aria-pressed={fullscreen} onClick={() => void toggleFullscreen()}>
              {fullscreen ? <Minimize2 aria-hidden="true" /> : <Maximize2 aria-hidden="true" />}
            </Button>
          </div>
        </div>
        {floatingError && <p className="px-3 py-2 text-caption text-destructive">{t(($) => $.vscreen.floating_failed)}</p>}
        <div
          className="relative min-h-0 flex-1 touch-none overflow-hidden bg-black"
          onPointerDown={startPan}
          onPointerMove={movePan}
          onPointerUp={endPan}
          onPointerCancel={endPan}
          onDoubleClick={() => { if (!controlling) resetView(); }}
        >
          {video.stream && binding ? (
            <div className="absolute inset-0 flex items-center justify-center overflow-hidden">
              <MirrorVideo
                stream={video.stream}
                onFrame={video.onFrame}
                ready={video.state === "streaming"}
                label={t(($) => $.mirror.frame_alt, { runtime: label })}
                fit={viewMode === "fit" ? "contain" : viewMode === "fill" ? "cover" : "actual"}
                zoom={zoom}
                pan={pan}
                interactive={controlling}
                onPointerDown={remotePointerDown}
                onPointerMove={remotePointerMove}
                onPointerUp={remotePointerUp}
                onPointerCancel={remotePointerCancel}
                onWheel={remoteWheel}
                onKeyDown={remoteKeyDown}
              />
            </div>
          ) : (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
              <Monitor className="size-7 text-muted-foreground" aria-hidden="true" />
              <p className="max-w-md text-caption text-muted-foreground">{status}</p>
              <Button size="sm" variant="outline" onClick={() => { void sources.refetch(); video.retryNow(); }}>{t(($) => $.vscreen.retry)}</Button>
            </div>
          )}
          {video.stream && video.state !== "streaming" && (
            <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-black/25 px-4 text-caption text-white">
              {video.reconnecting && retrySeconds !== null
                ? t(($) => $.vscreen.reconnect_countdown, { count: retrySeconds, attempt: video.retryAttempt })
                : status}
            </div>
          )}
          {controlling && gestureHintVisible && (
            <div className="absolute inset-x-3 top-3 z-20 flex items-start gap-2 rounded-md border border-white/15 bg-black/70 p-2 text-caption text-white shadow-lg">
              <Monitor className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
              <p className="min-w-0 flex-1">{t(($) => $.vscreen.gesture_hint)}</p>
              <Button size="icon-xs" variant="ghost" className="text-white hover:bg-white/15 hover:text-white" aria-label={t(($) => $.vscreen.dismiss_hint)} onClick={dismissGestureHint}>
                <X aria-hidden="true" />
              </Button>
            </div>
          )}
          <div className={`${fullscreen && !chromeVisible ? "hidden" : "flex"} absolute bottom-[max(0.75rem,env(safe-area-inset-bottom))] left-1/2 z-10 max-w-[calc(100vw-1rem)] -translate-x-1/2 flex-wrap justify-center gap-1 rounded-xl border border-white/15 bg-black/65 p-1 text-caption text-white shadow-xl backdrop-blur`}>
            <Button size="xs" variant="ghost" className="text-white hover:bg-white/15 hover:text-white" onClick={() => { setViewMode("fit"); setZoom(1); setPan({ x: 0, y: 0 }); }}>{t(($) => $.vscreen.view_fit)}</Button>
            <Button size="xs" variant="ghost" className="text-white hover:bg-white/15 hover:text-white" onClick={() => { setViewMode("actual"); setZoom(1); setPan({ x: 0, y: 0 }); }}>100%</Button>
            <Button size="xs" variant="ghost" className="text-white hover:bg-white/15 hover:text-white" onClick={() => { setViewMode("fill"); setZoom(1); setPan({ x: 0, y: 0 }); }}>{t(($) => $.vscreen.view_fill)}</Button>
            <span className="mx-1 h-5 w-px bg-white/20" aria-hidden="true" />
            <Button size="icon-xs" variant="ghost" className="text-white hover:bg-white/15 hover:text-white" aria-label={t(($) => $.vscreen.zoom_out)} onClick={() => adjustZoom(zoom - 0.25)}><ZoomOut aria-hidden="true" /></Button>
            <span className="min-w-10 text-center tabular-nums">{Math.round(zoom * 100)}%</span>
            <Button size="icon-xs" variant="ghost" className="text-white hover:bg-white/15 hover:text-white" aria-label={t(($) => $.vscreen.zoom_in)} onClick={() => adjustZoom(zoom + 0.25)}><ZoomIn aria-hidden="true" /></Button>
            <span className="mx-1 h-5 w-px bg-white/20" aria-hidden="true" />
            {controlling ? (
              <Button size="xs" variant="ghost" className="text-white hover:bg-white/15 hover:text-white" onClick={() => void control.stop()}>
                <Square aria-hidden="true" />
                {t(($) => $.vscreen.stop_control)}
              </Button>
            ) : controlState === "other_controller" && canManageControl ? (
              <Button size="xs" variant="ghost" className="text-white hover:bg-white/15 hover:text-white" disabled={control.pending} onClick={() => void control.stop()}>
                <Square aria-hidden="true" />
                {t(($) => $.vscreen.take_back_control)}
              </Button>
            ) : (
              <Button size="xs" variant="ghost" className="text-white hover:bg-white/15 hover:text-white" disabled={control.pending || !online || !readable} onClick={() => void control.request()}>
                <MousePointer2 aria-hidden="true" />
                {t(($) => $.vscreen.request_control)}
              </Button>
            )}
            <Button
              size="icon-xs"
              variant="ghost"
              className="text-white hover:bg-white/15 hover:text-white"
              aria-label={t(($) => $.vscreen.keyboard_capture)}
              aria-pressed={captureKeyboard}
              disabled={!controlling}
              onClick={() => setCaptureKeyboard((value) => !value)}
            >
              <Keyboard aria-hidden="true" />
            </Button>
            {compact && (
              <Button size="xs" variant="ghost" className="text-white hover:bg-white/15 hover:text-white" onClick={() => setInspectorOpen((open) => !open)}>
                <PanelRight aria-hidden="true" />
                {t(($) => $.vscreen.inspector)}
              </Button>
            )}
          </div>
          <div className="pointer-events-none absolute left-2 top-2 rounded-sm bg-black/55 px-1.5 py-0.5 text-micro text-white/75 lg:hidden">
            {t(($) => $.vscreen.rotate_hint)}
          </div>
        </div>
        <div className={`${fullscreen && !chromeVisible ? "hidden" : "flex"} shrink-0 flex-wrap items-center gap-2 border-t px-3 py-2 text-caption text-muted-foreground`}>
          <span className="flex items-center gap-1.5"><Wifi className="size-3.5" aria-hidden="true" />{networkHealth === "good" ? t(($) => $.vscreen.network_good) : networkHealth === "fair" ? t(($) => $.vscreen.network_fair) : networkHealth === "poor" ? t(($) => $.vscreen.network_poor) : t(($) => $.vscreen.measuring)}</span>
          {!compact && measuredStats && <span className="ml-auto tabular-nums">{quality.width} × {quality.height} · {measuredStats}</span>}
        </div>
      </div>
      {inspectorOpen && inspector}
    </section>
  );
}
