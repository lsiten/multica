import type { RuntimeDevice } from "../types";
import type { VscreenScope } from "../types/vscreen";

export const VSCREEN_CAPABILITIES = {
  video: "screen-mirror-video-v2",
  viewerGrant: "mirror-viewer-grant-v1",
} as const;

export interface VscreenAuthorization {
  readonly canReadRuntime?: boolean;
}

/** Authorization comes from the authenticated runtime read gate. */
export function deriveVscreenAccess(input: {
  readonly scope: VscreenScope;
  readonly runtime: RuntimeDevice;
  readonly authorization: VscreenAuthorization;
}) {
  const { scope, runtime, authorization } = input;
  const capabilities = runtime.metadata.capabilities;
  const has = (capability: string) =>
    Array.isArray(capabilities) && capabilities.includes(capability);
  const identityMatches =
    runtime.id === scope.runtimeId &&
    runtime.workspace_id === scope.workspaceId &&
    scope.accountId.length > 0;
  const online = runtime.status === "online" && Boolean(runtime.daemon_id);
  const readable =
    identityMatches && online && authorization.canReadRuntime === true;
  const canView =
    readable &&
    has(VSCREEN_CAPABILITIES.video) &&
    has(VSCREEN_CAPABILITIES.viewerGrant);
  return { canView };
}
