# Native app control

This package is the same-binary macOS input adapter. It is wired to the private native-host RPC and task-local daemon MCP. Linux, Windows, and builds without cgo return the explicit `unsupported_platform` error.

## Host integration contract

Create one `Controller` inside the native host with `New(Config)`. The native host must be running its existing AppKit main run loop; NSWorkspace launch is queued there and checks cancellation again before launch. The package does not create another app instance, global input service, or permission prompt.

Required callbacks:

- `Authorize(ctx, Authority, Access) (Display, error)` must verify the authenticated parent connection, the native display registry, current resource/native/display/geometry identity, and the native host's accepted task/transaction/lease fence. `ControlAccess` requires live task authority. `ObserveAccess` may instead accept a separately issued `ObserverGrant`. These callbacks do not query an in-process daemon Actor: the daemon checks its live actor before IPC, and the native host must implement its own grant/revoke fencing. A callback that always returns a display is not a production authorization implementation.
- `AuthorizeHuman(ctx, HumanRequest) (Display, error)` must validate and consume a separate opaque local Desktop-owner grant for that exact registered window and direction. It runs after the native quiescence barrier. A task token or ordinary GUI lease cannot replace this grant.
- Optional `CertifiedPIDInput(Process, protocol.VscreenAction) PIDInputDecision` must authorize only an independently tested application/OS/action combination, including the requested input mechanism and any required key/modifier subtype. PID eligibility reads the process executable path, validated dynamic code signature/CDHash and secured bundle version/build from the OS in addition to bundle ID and OS build. Base process/window identity remains independent of optional signing metadata and keyboard source. The production host installs `ProductionPIDInputPolicy`, whose immutable code-owned records are currently empty. Observations report `PIDInputCertificationConfigured` independently from `PIDInputVerification` (`none` or `verified_variants`); configured does not mean an App or action is certified. A decision binds the selected keyboard source once, and native dispatch rechecks it. Do not enable this predicate globally or solely because a symbol is available.

PID completion is a separate required gate: `Config.VerifyPIDCompletion(ctx, PIDCompletion) (PIDCompletionReceipt, error)` is injected only by code that can attest target processing and an independently checked effect. Without it, PID fallback is refused before posting even when a certificate matches; semantic AX remains available. `PIDInputCompletionAvailable` reports this wiring, and usable `PIDInputVerification` remains `none` without it.

An isolated, explicitly authorized fixture-qualification factory may install an invocation-scoped experimental input hook and completion verifier; this creates no production certificate, and its verification metadata remains `none`.

The native host creates a nonzero random 64-bit token bound to the complete action target, action/sequence, registered process and native window ID. Only the final key-up, left-mouse-up or scroll event carries it. Every posted PID operation retains a separate pending fence after pressed-button bookkeeping becomes empty. The verifier must return the exact binding plus `TargetProcessed` and `EffectVerified`; a current-context/authority check then permits the private backend `pid_complete`. Native clear rechecks the stored rich signed identity, window geometry and scope. No FD6/MCP/renderer completion switch exists, and tokens never enter Result. A confirmed completion returns `Mechanism=pid`, `Outcome=verified`, `CompletionVerified=true`; the native bridge reports AX separately.

Cancellation, wrong ACK, missing effect or changed identity before confirmation/clear retain the fence and block quiesce, recovery, transfer and claim release. If completion was already independently verified and cleared before cancellation, that fact stays confirmed; the request still returns an error and cannot revive old authority or replay input. No compensating re-pending is performed. Confirmed process exit also releases stale pending fences. The callback runs under the controller gate and must use an independent target oracle rather than recursively call this controller.

Public operations:

- `ListApps(ctx, Authority)` returns NSWorkspace-resolved bundle IDs, names and running state from bounded standard Applications folders. This inventory is incomplete when truncated and never certifies input support.
- `Launch(ctx, Authority, LaunchRequest{BundleID, Files}) (Window, error)` resolves an installed bundle through NSWorkspace, rejects running instances it cannot safely adopt, opens only explicit absolute file paths, and moves only the newly identified window. No shell command, caller-selected PID, clipboard content, or new-instance flag is accepted.
- `Observe(ctx, Authority, windowHandle, includePNG) (Observation, error)` returns up to 128 AX nodes, depth 12, with bounded text and fresh opaque element handles. A nonempty handle selects a registered window. An empty handle captures only the authorized virtual display. PNG is produced by SCScreenshotManager on macOS 14+, without audio/microphone capture. No real-screen fallback exists.
- `Act(ctx, protocol.VscreenActionRequest) (Result, error)` rechecks authority, window bounds, process incarnation, source geometry and snapshot revision before dispatch. Identical requests are cached without replay; new actions require a fresh observation after a mutation. Input is serialized with a three-second operation budget.
- `Quiesce(ctx, ResourceKey)` revokes immediately, cancels the matching in-flight request, then waits for native completion. `Resume(ctx, Authority)` is an explicit host-verified fresh grant after quiescence; it is not an automatic retry.
- `ListWindows(ctx, HumanRequest)` consumes a separate `list_existing` local-owner grant and returns up to 64 opaque, 15-second candidates bound to resource, epoch and intervention. It excludes ambiguous multi-window processes and claimed processes. Titles stay on the private local Desktop transport.
- `AdoptWindow(ctx, HumanRequest)` consumes an `adopt_existing` grant for the selected candidate, rechecks native identity/geometry and holds the process claim. It registers a human-owned window without moving or activating it. It requires a subsequent explicit `to_virtual` transfer before recovery. Neither operation is an Agent MCP tool.
- `HumanTransfer(ctx, HumanRequest{Grant, Resource, WindowHandle, Direction})` supports `to_real` and `to_virtual`. Only the authorized `to_real` path activates the app. Return requires fresh observation/recovery before new input.
- `Dispose(ctx, ResourceKey)` performs scoped quiescence, restoration and claim release for one runtime; sibling runtime windows and claims remain owned.
- `Close(ctx)` restores only automatically moved windows whose current identity and bounds still match. Missing original displays use a still-visible non-runtime display. User-moved windows are left untouched. Cleanup never closes documents or kills apps; failures retain claims and can be retried.
- `ProbePermissions(ctx)` reads AXIsProcessTrusted and CGPreflightScreenCaptureAccess only.

The native host must translate full `Window` records into opaque tool-facing handles. Do not permit a tool to send a raw Window/PID record back as authority. Opaque handles and their process/start/window mapping live only in this controller and native session.

## Native safety behavior

Process start time and UID come from `proc_pidinfo`, and claims are held using the existing AppClaim implementation under the OS account's actual home directory, `.multica/native-claims`. HOME overrides cannot split ownership. A window must be the process's sole AX window for background mutation; a frontmost app or a changed/foreign process/window refuses automatic input and movement.

Before each mutating primitive, the native adapter checks cancellation, process identity, the exact live display bounds and window ownership. App activation and login-session changes close native background guards. Window movement/resize/removal is rechecked before every action; propagation of native refusals into daemon/UI events remains part of host integration.

Semantic click uses AXPress only when the current element exposes it. Text uses AXSetValue only when settable, and a matching value readback reports `verified`; AXPress and per-PID event posting report only `dispatched`. Unknown elements never fall back to coordinate or focused-element input.

Certified per-PID code supports key, Unicode, click, scroll and drag using private CGEvent sources and CGEventPostToPid. It never posts to a global tap, warps the cursor, changes the pasteboard or raises a window automatically. Unicode fallback additionally requires the exact selected AX element to remain focused. Named key support is explicit: A–Z, 0–9, Enter/Return, Tab, Space, Escape, Backspace, Delete and ArrowLeft/Right/Up/Down; modifiers are shift/control/alt/meta. Other names return `needs_intervention`. ANSI key codes are not a promise of arbitrary keyboard-layout or IME compatibility.

Every posted down records its matching up and exact process incarnation. Cancellation/error attempts only the owned PID's release when that identity still matches. CGEventPostToPid has no delivery acknowledgement, so an interrupted pair remains uncertain even after a release attempt. Failed AX mutation replies also retain an uncertain native-operation fence because a timeout does not prove the remote app stopped. Quiesce, Resume, automatic cleanup and human transfer refuse while that process incarnation remains uncertain, and AppClaim stays held. Process exit clears the stale operation fence. This does not prevent the user from manually handling their own window; it must not be presented as a completed safe takeover.

## Verification boundaries

The default tests use a controlled backend to cover lease/snapshot guards, cancellation, no replay, separate human authority and claim retention, plus real nonprompt permission preflight and a denied native guard. The hidden fixture in `testdata/AppControlFixture.m` exercises its own explicitly implemented Cocoa accessibility button/value methods without external AX IPC or posted input. `--serve` creates the visible fixture for a later explicitly authorized external acceptance run; it is not part of default tests.

No real App/OS/action certification records are included. Actual installed-app launch/window movement, external AXPress/AXSetValue, SCScreenshotManager PNG, app-specific Unicode/IME/shortcuts, per-PID click/scroll/drag/key compatibility, foreground typing continuity and real input videos are not accepted yet. The certified matrix remains empty until those actual permission-enabled scenarios pass. Compiled methods and hidden self-observation do not replace that gate.

## Primary references

- [NSWorkspace.OpenConfiguration](https://developer.apple.com/documentation/appkit/nsworkspace/openconfiguration): activates, createsNewApplicationInstance and launch configuration. The installed AppKit header also states Gatekeeper UI is not suppressed by `promptsUserIfNeeded`; this adapter does not claim to bypass OS launch security UI.
- [AXUIElementSetMessagingTimeout](https://developer.apple.com/documentation/applicationservices/1459345-axuielementsetmessagingtimeout): per-element request timeout, not proof a timed-out target action stopped.
- [AXObserverCreate](https://developer.apple.com/documentation/applicationservices/1460133-axobservercreate): window-creation notification while identifying a newly launched window.
- [SCScreenshotManager](https://developer.apple.com/documentation/screencapturekit/scscreenshotmanager): exact-filter, on-demand image capture.
- Installed SDK `CoreGraphics.framework/Headers/CGEvent.h` defines CGEventPostToPid and CGPreflightPostEventAccess; `CGSession.h` defines the active-console/session keys. `sys/proc_info.h` defines `PROC_PIDTBSDINFO` start-time fields. No private SkyLight input symbols are used.

## Verified PID policy boundary

`ProductionPIDInputPolicy` is the only production record source; it does not load renderer, MCP or profile JSON. Each reviewed row binds bundle/version/build, signing ID/CDHash, exact OS build, input family and acceptance-evidence SHA256. Keyboard and Unicode rows also bind an input source; key rows bind the key and canonical modifier set. Dynamic text, coordinates, deltas and durations remain governed by the existing UTF-8/length/snapshot/window/bounds/time validation rather than by recorded-payload hashes. Element-handle clicks remain semantic AX actions; the current PID click implementation requires coordinates.

Before policy evaluation, the controller reads `pid_identity` from its native backend using the owned process. A mismatch with PID/start/UID/bundle/OS returns no eligibility. Metadata failure or missing signatures cannot confer PID authority and do not alter AX/candidate identity. The resulting host-owned decision and signed process identity never come from tool arguments. Before each PID event, native code re-reads the signed identity; key events also recheck the independent input source. A change refuses delivery. Unknown records continue to hand off; no production row has passed actual acceptance yet.
