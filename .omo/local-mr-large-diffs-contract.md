# Runtime-owned large MR implementation contract

Status: in progress. This document is an implementation checklist, not completion evidence.

## Additional user requirements (2026-09-10)

- Diagnose long-lived dialog getting stuck loading (user screenshot shows 1,054 files).
- Segment navigation must visibly position the requested segment, and advance to adjacent files when the current file has no remaining segment.
- Expand omitted unchanged context at hunk boundaries, as in the supplied reference; not merely show a full-file blob in another view.
- Stage/unstage selected working-tree files and commit staged changes on the owning runtime. Keep this distinct from historical branch-vs-target differences. Cloud must still relay only; no Git mutations performed during QA against user repositories.
- User explicitly declined actual desktop/browser operation. Such acceptance is not authorized and must be marked not performed, not used as a reason to keep requesting Computer Use permission.

### Navigation/loading progress

- A direct authorized metadata read of the screenshot's LSIT-17 worktree succeeded (HTTP 200, 1,054 files, 12.472s), without displaying its diff content. This does not establish the exact cause of the user's long-lived UI state.
- Reproduced an indefinite-loading path: React Query's default online mode pauses local IPC when the browser reports offline. Offline patch regression failed before setting MR queries/mutations to networkMode always, then passed. Remote failures now reach existing bounded transports rather than silently queueing; writes are not deferred until browser connectivity changes.
- File/byte segment changes recreate the scroll container so the new segment starts at the top. Previous/next falls through to neighboring files, lazily fetching the next metadata page when needed. Async next-file results do not override a different intervening file selection.
- Added regressions for offline local reads, file boundaries, crossing file-list pages and scroll reset. Context expansion and staging/commit implementation remain pending.

### Fixed-version context groundwork

- Added bounded original-file line reads with complete blob integrity verification. Tests cover exact line numbers, CRLF/CJK/final-line handling, omitted hunk ranges across patch pages, and rejecting tampering outside the selected range.
- Patch hunk metadata now carries omitted old/new context starts and counts. Added a read-only `context` action through daemon authorization, transient relay, typed core response parsing and shared platform helper; it uses the selected immutable version/file, not live file contents.
- Full localreview race suite passed before protocol wiring (8.094s); focused context tests and vet passed. UI expansion controls and staging/commit are not implemented yet; this section is groundwork, not completed user-facing context expansion.

### Inline omitted-context controls

- Hunk gap metadata now renders localized expand/collapse controls inside the patch. Each explicit expansion reads up to 50 fixed-version original lines, retaining both old/new line-number columns and stopping at the next hunk boundary rather than continuing through the file.
- Collapse unmounts the infinite-query reader (gcTime 0), so unfinished reads use the existing cancellation path. Gap-local loading/retry states do not block other files.
- Failing-first component test could not find an expansion affordance before wiring; it now passes. A 120-line gap test proves requests are 50/50/20 and stop at the boundary even when the file itself has more content.
- Context/file-browser/locale-parity suites passed 170 tests; views typecheck and targeted ESLint passed. No actual GUI operation was performed, per user instruction. The running daemon has not been rebuilt/restarted for the new context action yet; source implementation is not a live-deployment claim.
- Staged/Commit remains pending. Full-tail context and other edge cases still need final acceptance alongside the complete feature set.

### Working/index state foundation

- Added independent HEAD/index/worktree status reading for staging UI. It is not computed from the MR target diff: historical commits are excluded, mixed staged+unstaged paths retain both flags, untracked filenames preserve spaces/newlines, and renames retain source/destination.
- Conflicted paths and nested directory entries are identified explicitly. Invalid/path-escaping/truncated Git status records fail closed. Reads check source branch/head stability and use the existing read-only Git transport; mutation preconditions must still pin index/content bytes separately.
- Real temporary-repository tests verify staged/working separation, rename identities and byte-for-byte unchanged Git index after inspection. Parser regressions cover conflicts, nested directories and malformed records. This is read-only groundwork; no stage/unstage/commit command or UI is exposed yet.

## Required behavior

- Keep the three existing MR entrances, hide confirmed cleaned worktrees, and retain live completed worktrees.
- Search existing local target branches; default main, master, production, test, then first available. Explicit choices survive refresh.
- Read a paginated file manifest and additions/deletions without materializing every patch in one response.
- Load file patches on demand in bounded pages, with correct old/new line numbers across pages.
- Large/binary/generated content must not prevent other files from being reviewed. Explicit per-file states and content fallback, never silent omission.
- Runtime-owned immutable snapshots bind source, target, base, file paths/modes and content identity, including dirty/untracked files. Changed content invalidates approval; merge retains exact-ref CAS and conflict checks.
- No cloud MR tables, blobs or durable queue. Local desktop direct, remote requests via the existing authenticated relay.
- Preserve path/runtime/task authorization, replay safety, profile isolation, cancellation and bounded resource use.
- Reuse source/target locks and recovery receipts. Cache lifetime and cleanup must not remove data still needed by review/recovery.

## Implementation sequence

1. Streaming patch page reader and independent tests (page boundaries, line numbers, oversized single lines, cancellation).
2. Runtime snapshot manifest/content store and paginated manifest/file APIs, independent from full-patch rendering.
3. Route read/approve/merge/recovery through content identity, not the old 8 MiB full-patch hash.
4. Wire typed local IPC and remote relay actions; enforce response limits per page.
5. Shared file-list/detail UI, lazy loading, binary/oversize handling, search and explicit loading/error states.
6. Real Git + HTTP + IPC + database-auth relay tests, large mixed-file fixtures, and visual interaction acceptance.

## Evidence needed before completion

No whole-MR failure for more than 8 MiB total diff; bounded memory/response measurements; more than one file-list page; more than one patch page with stable line numbers; binary/very-long-line cases; stale approval rejection; CAS merge/recovery; direct and remote ownership rejection; read-only inspection leaves checkout/index unchanged; fresh UI captures and independent acceptance.

Existing unpublished work (inventory filtering, branch transport/default/search and error presentation) must be preserved and verified alongside this change. Production deployment/push is not implied by this implementation task.

## Current verified progress

- Streaming patch pages: bounded output, physical-line cursors, cross-page old/new line numbers, long-line refusal, cancellation. Focused and package race tests passed.
- Runtime blob cache: rooted filesystem access, SHA-256 content references, atomic publication, oversized/cancelled capture cleanup, full-stream integrity verification before returning a page. A patch above 8 MiB was stored and paged in tests.
- Version manifest storage: canonical file order, content/target-sensitive identity, validated file paths and content references, paginated file metadata and summary counts. 205-file pagination regression passed.
- Committed Git capture now records raw file identities, mode changes, binary flags and numstat counts, then streams immutable Git blobs into the cache. A real committed text change over 8 MiB was captured and its patch paged in tests; later working-directory edits did not leak into the captured patch.
- A requested text file patch is generated independently in a private temporary directory from verified cached bytes; repeated generation has stable identity. Oversized Git content is marked per-file with its immutable Git object ID while other files remain available.
- Working-tree capture now includes staged/unstaged net contents and untracked files, stores symlink text without following it, and checks source/target/status drift during capture. A same-status file edit changes the version ID. Oversized uncached working content keeps a full SHA-256 fingerprint instead of a prefix fingerprint.
- These modules are not yet wired to production MR reads or mutation revalidation. End-to-end transport, cache lifecycle, content fallback and lazy UI remain required. Special/nested-repository metadata-only handling also needs acceptance and explicit UI states.

## Read API integration progress

- New manifest/files/file actions now pass through the existing task-root authorization and daemon HTTP operation handler. Protocol/forwarder carry version ID, file path and bounded page parameters; server retains only transient response data.
- Real daemon forwarding tests cover 202-file pagination, more-than-8-MiB file patches, reading a pinned version after checkout edits, per-file binary/long-line fallback, and foreign file/task rejection.
- Existing branch and transient-relay tests still pass; tagged vet passed for daemon/handler. The tests above do not yet prove the server's full database-authenticated forwarding route for paged actions.
- Version-based approve/submit/request-changes/merge now use content-based revalidation and the existing exact-ref transaction. The original merge path retains its tests. New daemon tests cover large approval/merge, lost receipt recovery, duplicate approval, changed replay actor, stale-source rejection and refreshed draft state.
- Frontend/IPC consumers still use the old snapshot contract. Full server-authenticated paged requests, cache archival/lifetime and UI acceptance remain required before delivery.

## Typed transport progress

- Core now validates paged requests and responses, including version/file identity and pagination progress. API exposes the new relay payload without accepting legacy aggregate snapshots as pages.
- Desktop IPC resolves and verifies the owning profile/runtime before paged dispatch; foreign runtimes return to the relay, while local failures remain local errors.
- Server config and daemon health explicitly advertise paged review support. New clients refuse unsupported protocol versions rather than silently issuing aggregate reads.
- Platform routing and core/desktop boundary tests passed, including identity mismatch, unsupported capabilities and local-error routing. The visual dialog is not yet switched to these new APIs.

## UI integration progress

- New file-browser component consumes manifest pages separately from selected-file patch pages. Loading more filenames does not request their patches.
- Patch navigation renders one bounded page and disposes inactive patch queries; returning to a previous cursor reloads that fixed-version page. File/version keys prevent mixing selections.
- Binary/oversize/unsupported files have local states while the file navigation remains usable. Component tests cover lazy requests, next/previous segments and switching away from binary content.
- Existing shared tokens/primitives and accessibility constraints are recorded in packages/views/issues/DESIGN.md. Main-dialog wiring, commit/repository navigation preservation and fresh visual acceptance remain pending.

## Main dialog switched

- LocalReviewDialog now discovers repositories, resolves branch defaults/search, creates a paged manifest and uses the lazy file browser. Decisions carry the fixed version ID. Runtime discovery remains cached across target/repository changes.
- Commit history has its own pinned, paginated read action and loads only when expanded; repository discovery is read-only and preserves logical task paths for relay authorization.
- Updated task, agent/workspace and desktop worktree-manager component tests mount the real new dialog. Branch selection, runtime identity, version approval, two-step merge and merge-history labeling pass. Real Git tests prove commit pages do not include later source commits.
- The source UI is switched, but no release/restart or real-account visual acceptance is claimed. Cache archival/lifetime, content fallback, full server-authenticated paging acceptance and final hardening remain open.

## Cached-content fallback progress

- Cached file contents can be read independently from patches in bounded byte ranges. UTF-8 boundaries are extended by at most three bytes; binary data is rendered as hex. Integrity is verified before any range is returned.
- The content action preserves version/file/side identity through IPC/relay schemas and the owning daemon. UI supports before/after sides, previous/next ranges and returning to the diff without accumulating all content.
- Tests cover long lines, UTF-8 boundaries, binary ranges, tampering, frozen content after checkout edits and UI fallback navigation. Uncached files above the per-blob quota still need a verified source fallback; no claim of that capability yet.
- Cache archival/lifetime, full server-authenticated paging acceptance, cancellation/performance hardening and real visual acceptance remain open.

## Archive safety progress

- Review records/events now retain explicit version references and manifests identify which sides were actually cached.
- Archival copies referenced manifests and required cached content, including historical referenced versions, but not unreferenced scratch. Missing required content fails archival instead of silently losing it.
- Cleanup now archives before removing a Git worktree. A regression toggle reproduced the old behavior deleting the checkout before discovering missing archive content; restored ordering passes.
- Archive files are published atomically, receipt reads reject symlink replacement, and receipt hashes are rechecked after content archival. Tests prove archived content survives source-task removal.
- Capacity/expiry policy, active-read retention, uncached-source fallback, full server-authenticated acceptance and visual acceptance remain open.

## Uncached content fallback progress

- File content requests now use verified cache data first. Missing/uncached committed bodies are read from the exact tree/object mapping and checked against Git's object digest (or the captured SHA-256 when available).
- Uncached working bodies are returned only after their full captured SHA-256 and size match; changed live contents return an error with no partial response. Symlinks return their stored link text, never target-file contents.
- Git replacement objects are disabled for review inspection/object reads so they cannot redefine a pinned object. Integrity failures are not silently masked by fallback.
- Focused/full localreview race tests and vet passed; quota-simulation tests cover uncached committed and working files. Cache capacity/expiry, active-reader retention, full server-authenticated acceptance and visual acceptance remain open.

## Cache policy progress

- Cache admission now has a 1 GiB per-task budget with 32 MiB reserved for metadata; allocation-unit charging bounds tiny-object counts. Duplicate objects do not consume new budget.
- Version catalog markers separate registered manifests from arbitrary content blobs. Reads/decisions renew version access timestamps without changing snapshot identity.
- Mark/sweep planning validates references before deletion, preserves explicitly protected versions and fresh leases, and expires unreferenced content/scratch. Budget pressure does not override protection; quota satisfaction is reported explicitly.
- Data admission failure retains complete working fingerprints and exposes an uncached per-file state; the metadata reserve can still publish the manifest.
- Policy, deduplication, lease/record protection and metadata-reserve tests pass. Automatic maintenance/record-reference discovery and UI heartbeat integration remain pending; do not claim those are active yet.

## Runtime maintenance and active reads

- Runtime requests and idle-root GC now invoke bounded, throttled cache maintenance after discovering current/history/prepared version references from receipts. Maintenance failure is logged and does not bypass protections.
- Initial captures acquire a short-lived in-flight marker; loaded versions renew a five-minute lease. Open dialogs renew the displayed version every minute and stop their query timer when unmounted.
- Worktree cleanup skips fresh review leases and exposes a distinct recently-reviewed protection reason. Tests prove initial capture blocks deletion and releasing it allows cleanup again.
- UI heartbeat, runtime lease, reference discovery, cleanup protection, race/vet and typechecks pass. Full server-authenticated acceptance, cancellation/performance hardening and fresh visual acceptance remain outstanding.

## Authenticated cross-process paging acceptance (2026-09-09)

- Created isolated PostgreSQL database `multica_mr_paged_20260909_qa01`, migrated through 455, and ran `TestLocalReviewAcrossHTTPDatabaseAndRuntimeProcesses` with the agent-CLI guard and `-race -count=1 -v`.
- Both legacy (3.98s) and paged (12.55s) scenarios explicitly ran and passed; total test time 16.53s. The test uses production JWT/membership middleware, real HTTP handlers and an independent runtime test process for every command.
- The >8 MiB fixture passed repository discovery, bounded manifest, read lease, consecutive patch pages, fixed content, pinned commit history, approval, merge and merge redelivery. Git target matched the returned merge commit and source checkout remained unchanged.
- Invalid JWT, unauthenticated spoofed identity and cookie writes without CSRF were rejected. The migrated schema contains none of the three asserted cloud MR business tables; this table check alone is not proof that arbitrary logging/storage can never retain payloads.
- Dropped only the isolated test database after successful completion. No deployment, release or desktop restart was performed.
- Remaining gates include the wider authenticated isolation/error matrix, cancellation/performance hardening and fresh visual acceptance. This is integration evidence, not complete UI or production acceptance.

## Cancellation-path audit and full package regression (2026-09-09)

- Guarded `go test -race ./internal/daemon/localreview ./internal/daemon/execenv -count=1` passed both complete packages (29.988s and 68.301s), not only a selected MR test expression.
- Cancellation is not end-to-end yet: dialog/file/content query functions do not consume React Query's signal; `requestReviewPage` has no signal parameter; API calls use timeout-only signals; desktop paging IPC carries no cancellation identifier.
- `reviewOperationHandler` and `recoverRemoteMerge` wait on the same plain `sync.Mutex`. A disconnected/cancelled caller cannot abandon that lock wait. Idle maintenance already uses TryLock and is not the same gap.
- The relay removes pending exchanges when the HTTP context ends, but this does not notify an already-claimed runtime command. Read cancellation must propagate across this seam; mutating-command receipt recovery must remain intact.
- `FilePatch` reconstructs both temporary sides and invokes Git on each page request. Paging bounds returned content but does not yet avoid repeated generation. These are confirmed implementation gaps, not completed optimizations.

## Cancellable daemon operation gate

- Regression `TestReviewRecoveryCancelsWhileAnotherOperationHoldsLock` failed on the original mutex with `cancelled recovery remains blocked on review gate`.
- Replaced only the global operation gate with the existing x/sync weighted semaphore (capacity one). Both review handler and merge recovery acquire it using the caller context; idle maintenance retains nonblocking TryLock semantics. No receipt/state/merge transaction behavior was changed.
- Regression now passes; an additional test proves an already-cancelled acquisition neither consumes capacity nor permits overlapping operations. Both tests passed ten race-enabled repetitions. Selected paged/remote/local-review daemon tests passed (14.266s), and `go vet ./internal/daemon` passed.
- The gate owns exclusion only; HTTP/IPC/UI cancellation propagation and claimed-relay-command cancellation remain open. The database-backed cross-process scenario was not rerun in this step.

## Shared read and HTTP cancellation plumbing

- Added optional AbortSignal plumbing to paged-review read helpers, capability discovery and API execution. HTTP combines the reader signal with existing 15s/55s deadlines instead of replacing deadlines.
- Already-cancelled reads are rejected before local or remote dispatch. Cancellation during local-runtime discovery prevents subsequent cloud fallback; cancellation during capability discovery prevents the page request.
- New tests failed before implementation (ignored cancellation and missing signal forwarding) and passed after it. Core API suite: 7 passed; shared platform suite: 8 passed. Core and views typechecks passed.
- These changes provide transport plumbing only: React Query call sites have not yet supplied their signals, and active desktop IPC/claimed remote commands still need cancellation propagation. No end-to-end UI cancellation claim is made.

## Desktop IPC read cancellation

- Added a bounded (32 pending reads per window), window-scoped cancellation registry. It removes registrations/listeners on settlement and aborts reads on WebContents destruction. Another window cannot cancel a matching identifier.
- Preload exposes read identifiers and a cancel event. Shared read plumbing installs/removes its AbortSignal listener and sends the same identifier when cancelled. Main registers only non-decision actions; approvals/merges are not registered as cancellable reads.
- The selected-profile transport now passes cancellation through runtime discovery, health checks and authenticated daemon HTTP reads, combining it with existing deadlines. Cancellation after discovery/profile selection prevents further dispatch.
- Desktop cancellation/routing suites: 11 passed. Shared platform suite: 9 passed. Desktop node/web and views typechecks passed. Tests cover window isolation/destruction, listener cleanup, exact identifier forwarding and transport propagation; no native Electron interaction acceptance claimed.
- React Query callers still need to consume their signals, claimed cloud commands still need read cancellation notification, and diff generation reuse remains pending.

## Query lifecycle cancellation wiring (2026-09-10)

- Paged repository/manifest/file-list/patch/content/commit/lease query functions now consume React Query's AbortSignal and pass it into the existing platform/HTTP/IPC chain. Decision mutations remain unchanged.
- The unmount regression failed before wiring because no signal reached the patch reader; it passes after wiring. A second component test proves selecting another file aborts the old reader while leaving the newly selected reader active. Test-created deferred responses are settled during cleanup.
- Dialog and file-browser tests passed (12 tests before adding the file-switch regression), and views typecheck passed. No markup, style, copy or visual design changes were made in this step; native/browser visual acceptance remains outstanding.
- Legacy branch discovery has not yet joined the cancellable paging protocol. Already-claimed cloud commands, diff generation reuse, and final end-to-end acceptance remain open.

## Reuse generated file patches

- Added a runtime-process-only FIFO index capped at 256 patch references. Keys include the rooted task cache, both content fingerprints/sizes, presence and Git modes. No diff bytes or index records are persisted in the cloud.
- FilePatch reuses a generated patch after full blob integrity validation. Reopening the task BlobStore retains reuse within the same process. Missing/GC'd blobs regenerate; malformed/tampered bytes fail closed. Restarts and index eviction can regenerate safely.
- Extended the captured-content test to reopen the store and remove Git from PATH after initial generation: it failed before implementation with executable-not-found and passes afterward, proving a hit does not regenerate through Git.
- New tests cover bounded reference retention, missing-patch regeneration and same-size tampering rejection. Full `go test -race ./internal/daemon/localreview -count=1` passed (7.386s); package vet passed. Test durations are not a controlled performance benchmark.
- Integrity verification and patch cursor scanning still read the patch stream; this change removes repeated Git generation, not all per-page I/O. Remaining work includes remote claimed-read cancellation, branch cancellation, large-repository performance measurement and final acceptance.

## Claimed remote read cancellation

- Server claims now advertise cancellation support; a runtime-authenticated status endpoint returns only the transient exchange's active boolean, scoped by workspace/runtime/claim token and marked no-store. Caller disconnect removes the existing exchange; no cancellation queue or MR table was introduced.
- The runtime loop wraps advertised read actions in a bounded 45-second context and a joined watcher. It checks claim liveness at one-second intervals with two-second HTTP deadlines, cancels on explicit inactive status, and skips result retries for cancelled reads. Errors/missing active fields do not imply cancellation. Decisions and older non-advertising servers bypass the watcher.
- New runtime HTTP test proves claim/path forwarding and watcher shutdown on inactive status. Relay test proves active state disappears after disconnect and cannot be queried by foreign workspace/runtime/token.
- An isolated PostgreSQL database was migrated through 455 so handler tests could actually execute: five selected relay tests explicitly RUN/PASS; the temporary database was dropped afterward. These relay tests are in-memory state tests despite the package's DB bootstrap, not full authenticated status-endpoint acceptance.
- Selected daemon race tests passed (7.681s); daemon/handler vet passed. Full UI-to-authenticated-status-endpoint cancellation acceptance, legacy branch cancellation, large-repository performance and final visual acceptance remain open.

## Status authorization and real-process regression

- Added DB-backed status-handler tests for owner credential, owning daemon, other member, other daemon, other runtime, and incorrect claim token. All six explicitly ran and passed under `-race`; only authorized matching claims expose active status.
- The real JWT/HTTP/PostgreSQL/separate-process MR scenario now executes `runClaimedReview` and registers the authenticated status route. The paged scenario asserts at least one runtime status HTTP call occurred, so the watcher cannot silently be bypassed.
- Both full scenarios passed with `-race -count=1 -v`: legacy 3.97s, paged 12.25s, total 16.23s. The >8 MiB read/approve/merge/replay flow remains successful with cancellation monitoring enabled.
- Test database `multica_mr_status_20260910_qa01` migrated through 455 and was dropped after tests. This proves the active-request status route and normal-flow integration; browser-driven disconnect through the complete deployed stack is still not claimed.

## Branch query cancellation

- Branch queries now consume React Query signals through capability discovery, HTTP relay, desktop preload/main and the selected-profile daemon transport. They use the existing window-scoped read registry and remote read watcher; no second cancellation system was added.
- Extracted shared platform IPC cancellation into one helper used by both branch and paged reads. Listener lifetime, request identifiers and no-relay-after-abort behavior remain covered by platform tests.
- Branch regression failed before wiring because a cancelled local lookup proceeded to relay and resolved; it now rejects without cloud fallback. API coverage verifies cancellation reaches the branch HTTP signal while preserving the branch-only payload.
- Views/platform/dialog suites passed 21 tests; core API suite passed 8; desktop routing/cancellation suites passed 11. Desktop node/web and views typechecks passed. This was lifecycle plumbing only, not a visual redesign or native runtime acceptance.
- Remaining acceptance includes complete browser-disconnect execution, controlled large-repository performance, broad final regressions and fresh UI/visual evidence.

## Controlled 1,001-file scale fixture

- Added opt-in `TestReviewScaleThousandFilesAndLargePatch` (`MULTICA_REVIEW_SCALE_TEST=1`) using disposable Git/task roots only. It creates 1,000 distinct ~45 KB files and a 12.5 MB text file, then tests both committed and uncommitted-working capture, metadata pagination and a deep patch page.
- Two race-enabled runs passed. The final run measured committed capture 8.577s, fresh-cache working capture 5.850s, first patch generation 161.8ms, reused-patch page at physical line 250,000 (200 rows) 573.3ms. Total test 16.32s.
- Generated patch 13,500,094 bytes; first 100-file metadata page 36,698 JSON bytes; deep patch response 13,846 JSON bytes. The test asserts file count, dirty-working inclusion, >8 MiB patch size, continued pagination and <=256 KiB fixture response sizes.
- Measurements are local macOS race-instrumented fixture results, not universal latency guarantees or a browser/deployed-network benchmark. They establish this workload fits the existing read deadlines without aggregate-diff rejection; larger/wider/long-line workloads still require their explicit bounds and fallback tests.

## Combined entry-point regression and desktop compilation

- Combined views runs exposed three stale argument assertions across the task MR entry, desktop worktree-manager entry and main-process branch transport after adding AbortSignal/read identifiers. Updated those assertions to require the new arguments while preserving task/runtime/path and no-cleanup expectations.
- Rerun results: shared views/platform/entry suites 48 tests passed; desktop routing/inventory/worktree suites 34 passed; core local-review suites 28 passed. Core typecheck passed.
- `pnpm --filter @multica/desktop exec electron-vite build` succeeded for main, preload and renderer. This only compiled desktop assets; it did not package an installer, update the daemon binary, push code or publish images.
- Build warnings remain: CSS optimizer does not recognize the two existing find-highlight pseudo-elements; emoji-picker is both dynamically and statically imported. Renderer main chunk is approximately 15 MB uncompressed, so compilation is not evidence of frontend performance acceptance.
- Native/browser visual and real-account acceptance remain unproven; do not mark the implementation complete based on these tests/build alone.

## Static/build verification and native permission gate

- `go build ./cmd/server ./cmd/multica` passed. Targeted ESLint passed for shared review transport/cancellation/dialog/file/content components and desktop cancellation/request transport.
- CUA inventory identified the running Multica Electron app. Selecting its exact repo-local app path returned `Computer Use permissions are not granted`; no current native screenshot or interaction evidence was obtained. No alternative native control mechanism was used to bypass this denial.
- Visual-qa completion remains blocked on authorized current-surface access and fresh independent review. Nonvisual checks above do not substitute for that gate. The goal remains incomplete; no release/commit/push occurred.
