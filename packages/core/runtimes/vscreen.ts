import type { RuntimeDevice } from "../types";
import type { VscreenScope, VscreenStateResponse } from "../types/vscreen";

export const VSCREEN_CAPABILITIES = {
  virtualScreen: "virtual-screen-v1",
  backgroundInput: "background-input-v1",
  video: "screen-mirror-video-v2",
  viewerGrant: "mirror-viewer-grant-v1",
  screenControl: "screen-control-v1",
} as const;

export interface VscreenAuthorization {
  readonly canReadRuntime?: boolean;
  readonly canManageRuntime?: boolean;
}

/** Authorization comes from the authenticated runtime gate; native permissions are separate observations. */
export function deriveVscreenAccess(input: {
  readonly scope: VscreenScope;
  readonly runtime: RuntimeDevice;
  readonly authorization: VscreenAuthorization;
  readonly observation: VscreenStateResponse | null | undefined;
}) {
  const { scope, runtime, authorization, observation } = input;
  const capabilities = runtime.metadata.capabilities;
  const has = (capability: string) =>
    Array.isArray(capabilities) && capabilities.includes(capability);
  const identityMatches =
    runtime.id === scope.runtimeId &&
    runtime.workspace_id === scope.workspaceId &&
    scope.accountId.length > 0;
  const online = runtime.status === "online" && Boolean(runtime.daemon_id);
  const supported =
    has(VSCREEN_CAPABILITIES.virtualScreen) &&
    has(VSCREEN_CAPABILITIES.viewerGrant);
  const readable =
    identityMatches && online && authorization.canReadRuntime === true;
  const canView =
    readable &&
    has(VSCREEN_CAPABILITIES.video) &&
    has(VSCREEN_CAPABILITIES.viewerGrant);
  const isOwner = runtime.owner_id === scope.accountId;
  const ownerAuthorized =
    readable && supported && isOwner && authorization.canManageRuntime === true;
  const state =
    observation?.runtimeId === scope.runtimeId &&
    observation.workspaceId === scope.workspaceId
      ? observation.state
      : undefined;
  const knownState =
    state !== undefined &&
    state.state !== "unknown" &&
    state.controlState !== "unknown";
  const screenRecordingGranted =
    state?.permissions.screenRecording === "granted";
  const accessibilityGranted = state?.permissions.accessibility === "granted";
  return {
    canView,
    canEnable:
      ownerAuthorized &&
      knownState &&
      state.state === "disabled",
    canDisable:
      ownerAuthorized &&
      knownState &&
      ["ready", "suspended", "creating"].includes(state.state),
    canRequestTakeover:
      ownerAuthorized &&
      knownState &&
      state.state === "ready" &&
      state.controlState === "agent",
    screenRecordingGranted,
    accessibilityGranted,
    // Returning control requires the separate intervention receipt, not display state.
    canReturnToAgent: false,
  };
}
