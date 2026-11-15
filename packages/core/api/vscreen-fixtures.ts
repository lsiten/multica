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
export const sourceWire = {
  resource: {
    backend_identity: scope.backendIdentity,
    workspace_id: scope.workspaceId,
    runtime_id: scope.runtimeId,
    uid: 501,
  },
  source: { kind: "physical", source_id: "display-1" },
  native_epoch: "native-1",
  generation: "capture-1",
  primary: true,
  name: "Primary display",
  width: 1600,
  height: 900,
  scale: 2,
  geometry_revision: 1,
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
