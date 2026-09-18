export const scope = {
  backendIdentity: "https://api.example.test",
  accountId: "user-1",
  workspaceId: "ws-1",
  runtimeId: "runtime-1",
};
export const envelope = {
  workspace_id: scope.workspaceId,
  runtime_id: scope.runtimeId,
  daemon_generation: "daemon-1",
  request_id: "request-1",
};
export const stateWire = {
  ...envelope,
  state: {
    runtime_id: scope.runtimeId,
    state: "ready",
    native_epoch: "native-1",
    display_generation: "display-1",
    geometry_revision: 2,
    control_state: "agent",
    active_task_id: "task-1",
    intervention_id: null,
    permissions: { screen_recording: "granted", accessibility: "granted" },
    state_revision: 3,
  },
};
export const sourceWire = {
  resource: {
    backend_identity: scope.backendIdentity,
    workspace_id: scope.workspaceId,
    runtime_id: scope.runtimeId,
    uid: 501,
  },
  source: { kind: "virtual", source_id: "display-1" },
  native_epoch: "native-1",
  generation: "capture-1",
  primary: false,
  name: "Virtual",
  width: 1600,
  height: 900,
  scale: 2,
};
export const grantWire = {
  grant_id: "grant-1",
  session_id: "session-1",
  workspace_id: scope.workspaceId,
  runtime_id: scope.runtimeId,
  user_id: scope.accountId,
  viewer_id: "viewer-1",
  native_epoch: "native-1",
  source: sourceWire.source,
  source_generation: "capture-1",
  expires_at: "2099-01-01T00:00:00Z",
};
