# Private app RPC

`hostclient.Start(Config{AppControl: true})` enables FD5 media and FD6 app requests.
FD3 lifecycle/capture control retains its serialized cancellation semantics. FD6
is a separate inherited socket authenticated with the FD4 bootstrap capability,
same-UID peer credentials, and inode separation from FD3/FD5. It is never exposed
to an Agent. Only the daemon may mint task, observer and local-owner grants after
its own actor/task/Desktop checks.

FD6 uses the existing bounded request/response envelope (`AppRequest`,
`AppResponse`). Decimal request IDs strictly increase. At most 16 operations may
be in flight; a single reader handles grants, cancellation and revoke fencing
before starting bounded operation workers. Native controller work remains
serialized with its three-second budget. Cancellation never aborts FD3 or kills
the helper. Exact-authority late cancellation also fences a just-completed call;
it cannot fence a newer task/lease. EOF fences all leases, joins native calls and
restores owned windows. Failed restoration prevents display destruction and
claim release on FD3 cleanup.

Task flow: Grant (relative TTL <= 15 seconds) -> ResumeApps -> ObserveApp ->
ActApp/LaunchApp -> Renew or Revoke. First grant also needs explicit ResumeApps.
A new owner needs successful QuiesceApps/Revoke and strictly higher LeaseEpoch.
Expired/revoked leases cannot renew. Resume is one attempt per lease epoch;
unknown native outcomes retain the controller's native uncertainty fence.
QuiesceApps/DisposeApps use the full last Authority even after expiry. Existing
FD3 quiesce/dispose invoke the same controller barriers using resource/epoch.

GrantObserver uses an opaque ObserverGrant with empty task/transaction/lease
fields. Observe-only authority cannot dispatch input. RevokeObserver cancels
only that observer's reads. GrantHuman accepts a separately minted capability,
intervention ID, exact window, direction and catalog source ID. Capability reuse
is rejected; consumption precedes native movement. Real destinations exclude
managed and resource displays and require fresh geometry/UUID readback.
Successful TransferApp is native movement evidence, not a daemon resume proof.
The daemon must establish its own intervention/return proof and fresh task lease.

PNG is never JSON/base64 on FD3 or FD6. Media flag 4 (`MediaSnapshot`) uses the
existing 120-byte media header: stream ID is the 16-byte snapshot nonce, PTS and
duration slots carry unsigned offset and total size, and payload is PNG bytes.
Chunks are <= 64 KiB, total <= 8 MiB. Each chunk locks the existing media writer
separately so video/terminal delivery can interleave. Video flags 0/1 and terminal
flag 2 retain their original meanings. Snapshot data never appears in AnnexB.
The control descriptor binds nonce, resource, full epoch, display, window,
snapshot revision, size and SHA256. Client registration precedes the request;
bulk and response can arrive in either order. Exposure requires complete ordered
assembly and matching descriptor/hash/PNG signature. Missing image, bad hash,
wrong identity, duplicate/out-of-order chunks and EOF never yield metadata-only
success. Client pending storage is capped at four snapshots; replay tombstones
and host snapshot/human capabilities are capped at 4096 per helper incarnation.
Unknown/late snapshot IDs allocate no storage. Framing corruption closes FD5;
semantic snapshot failures affect only that snapshot. FD3 stays independent.

AX nodes and strings are deterministically bounded, then trimmed to a 40 KiB
encoded observation budget with `Truncated=true`. Error codes are allowlisted;
input text, PNG and native diagnostic strings are never logged.

Default verification uses fake controller/process/socket fixtures only. The
actual same-binary controller is wired, but no GUI, TCC prompt, display creation,
installed-app manipulation, posted input or real Agent CLI is exercised here.
Native permission-enabled end-to-end acceptance remains outstanding.

## Managed window reuse

`ManagedAppWindows(ctx, authority)` uses the private `app_managed_windows` RPC.
The response is `managed_windows: [{window_handle, bundle_id}]`, capped at 128
entries / 48 KiB. It requires the current resumed task lease and exact resource
and epoch; observer grants cannot enumerate it. Native Controller ownership is
the source of truth. Human-owned, released, foreign, closed-process/closed-window
and out-of-display entries are excluded using current window-server metadata;
no AX tree/screenshot, movement, activation or ownership transfer occurs.
Listing never registers an input proof and does not mark every window used by a
task. Resume invalidates old window snapshot revisions. A fresh explicit window
observation remains mandatory before any action; metadata can become stale after
listing and observation revalidates native identity and geometry.

Production MCP acquire/status expose the same directory for the current lease.
Display-only observation returns `observation_scope: "display"`, an empty
`window_handle`, `snapshot_revision: 0`, PNG and `managed_windows`; it never
registers a fake empty-window observation. Successful launches and explicitly
selected known managed handles are tracked before PNG capture, so a screenshot
failure cannot hide their real ownership from human intervention. Subsequent
Agents and continuations discover preserved windows through production output,
without injected fixture handles or persistent PID tables.
