# Local MR implementation status

HTTP-origin ID compatibility fixed: MR mutations now use core's existing
`createSafeId` instead of directly calling crypto.randomUUID. A regression first
failed when randomUUID was unavailable, then verified secure getRandomValues
fallback produces the operation ID. With no secure randomness, mutation dispatch
is refused. Eight routing tests, 22 selected utility tests and views typecheck
passed. This does not claim plain HTTP protects authentication transport.

Later legacy/profile delta re-review completed: security PASS; code review found
runtime identity tied to target/path snapshots. It is now stored in an observed
task/workspace Query entry, so target/repository changes retain discovered local
identity. Both mounted switching regressions pass; fresh independent cache review
PASS is recorded in `.omo/evidence/mr_runtime_cache_recheck.md`. No style changes
were part of this fix. Overall completion still follows LOCAL_MR_ACCEPTANCE.md.

Entry integration coverage strengthened: task and shared agent/workspace entry
tests no longer replace LocalReviewDialog with a placeholder. They mount the real
dialog, assert selected-task diff content/runtime identity, and exercise approval
from the task entry. Desktop WorktreeManager likewise opens the real dialog using
its daemon boundary and asserts review does not invoke cleanup. Seven selected
entry/manager tests plus views and desktop typechecks passed. These are component
integration tests with transport boundaries, complementary to real HTTP/JWT/Git
and Electron IPC tests; they are not a production account navigation recording.

History presentation corrected: completed merge and merge_recovered events now
show the existing localized merged label, not merge-requested/submitted. Both
regressions failed before the fix, then all five dialog tests passed. visual-qa
captured six fresh settled states (closed/open at 375/768/1280) and both independent
reviewers passed this narrow delta; see `.omo/evidence/mr-history/visual-verdict.md`.

Desktop production-mode compilation passed for main, preload and renderer in
isolated output `/tmp/multica-mr-build.SGH12A`; this did not overwrite running dev
output and is not a packaged app/CLI release. Knip found a direct undeclared zod
import in desktop routing; moving that response schema to core fixed the package
boundary and knip passed. Static imports removed the MR module's ineffective
dynamic-import warning. Nine focused desktop/core tests and views typecheck
passed. Existing highlight CSS and emoji-picker dynamic-import warnings remain.
The isolated output is retained for follow-up acceptance, not deployment.

Legacy cleanup-manager entries can now discover runtime identity by task ID via
an owner-only metadata endpoint. Main validates task/workspace identity, verifies
the discovered runtime against local health, and returns the runtime ID with the
snapshot. The dialog reuses that ID for later reads/actions; first local mutation
can persist the verified legacy binding using the previously tested path. Tests
cover once-per-known-identity discovery, wrong-workspace rejection, and owner/
nonowner discovery against a fresh DB. Desktop typecheck, 5 desktop tests, 7 view
tests and 4 core API tests passed. The temporary DB was removed; full app-entry
acceptance is still not claimed.

Legacy local mutation support: when a task entry supplies an owning runtime ID
but its directory lacks `.review-runtime.json`, daemon verifies the tuple through
an owner-only read endpoint backed by existing task metadata. It persists the
verified binding under the task lock; subsequent local mutations do not repeat
the cloud lookup. Mismatched identity is rejected without writing provenance.
This one-time metadata verification is not Git-data forwarding. New directories
remain local-only. Real localhost legacy tests passed, and fresh-database owner/
other-member/wrong-runtime endpoint tests passed under race detection. The test DB
was removed. Cleanup-manager legacy rows lacking any runtime ID still need a
discovery path; this does not claim all legacy entrances complete.

The independent-worker integration now uses production `middleware.Auth` JWT
verification, workspace membership and human-actor gates instead of directly
stamping identity. It passed with a fresh temporary database and test signing
key: valid JWT read/approve/merge updates real Git, client-supplied user identity
is overridden, missing auth/wrong signatures are rejected, and Cookie writes
without CSRF are rejected. No cloud MR tables exist. This proves credential
validation on the HTTP/worker path, not the application's sign-in UI or deployed
user-session behavior. The temporary database was removed after the run.

Isolated Electron IPC acceptance now passed using current production preload
and main request code, real Electron IPC, and a localhost fake daemon. Read,
approval, foreign-runtime routing and malformed-action rejection are captured in
`.omo/evidence/mr-electron-ipc.md` and its screenshot. This closes the basic
renderer/preload bridge evidence gap, not full sign-in/navigation/deployed-network
acceptance. The fixture process was separate from the user's desktop and stopped.

Desktop MR request orchestration is now independently testable in
`apps/desktop/src/main/local-review-request.ts`. The IPC registration uses this
function and pins the selected profile through health validation and the local
request, instead of resolving a potentially changed profile twice. An ephemeral
real HTTP fixture verifies profile headers, request payload, response parsing,
and one profile selection; another-runtime routing never issues the local POST.
Three focused desktop tests and both desktop typechecks passed. This is main-
process transport evidence, not an Electron renderer/preload end-to-end run.

Latest verification: filter security delta independently passed
(`.omo/evidence/mr_filter_security_recheck.md`). A fresh PostgreSQL handler run
with explicit DATABASE_URL and verbose output executed 18 authorization/claim/
relay/operation-ID scenarios successfully under race detection. Earlier handler
exit-0 results without a reachable DB were skipped by TestMain and are not test
execution evidence. These handler tests use trusted actor stamps, not production
login credentials. Overall UI/authenticated acceptance remains incomplete.

## Latest security remediation awaiting independent re-review

Git status/diff inspection now reads filter command names and applies invocation-
local empty clean/process overrides plus required=false. The shared worktree
inspection wrapper uses the same bounded configuration helper. No repository
configuration is rewritten. The comparison deliberately uses raw working content,
including for files normally converted by LFS/custom filters; it does not claim
equivalence to running arbitrary configured conversion programs.

Real clean/process marker tests prove commands are not run, raw file changes
remain in the diff, and original filter configuration is unchanged. Guarded
localreview/daemon race suites passed. This fixes the reproduced configuration
case; it is not a claim of protection from an OS user concurrently replacing Git
configuration/executables. The independent security gate remains pending rather
than silently inheriting a PASS from implementation tests.

## Required behavior

Any authenticated Web or Desktop client can view, submit, and decide a local MR.
The owning runtime stores MR state, decisions, history, and merge receipts.
An owning-machine client accesses its daemon directly; other clients use the
server only for authorized request/response forwarding. The server must not
persist MR diffs, snapshots, approval history, or a durable MR command queue.
Only existing identity, permission, and runtime-routing metadata is required
centrally. Offline runtimes are unavailable, not replaced by cloud snapshots.
No GitHub PR is created. The three entry points are issue
worktrees, agent Work tab worktrees, and worktree management.

## Current status after removing cloud persistence

The old cloud MR routes, four legacy client API methods, persistent command
consumer endpoints, MR schema migrations 455–463, SQL mutations and generated
models are removed. Task MR entry reads existing task metadata rather than a
cloud MR list. Only the worktree inventory SELECT remains in SQL.

The independent-process HTTP/PostgreSQL/Git scenario passed with race detection
on a freshly migrated database containing none of the three cloud MR tables.
Read, approve and merge completed in three separate runtime processes; target
HEAD matches the local receipt and source HEAD is unchanged. sqlc generation,
handler/server compilation, focused view tests and desktop typecheck passed.
Authentication in this process scenario remains a fixture, not production-login
acceptance. No user database or deployed server was modified.

Still incomplete: legacy runtime provenance and reused-task inventory/lifecycle
acceptance; multi-instance ephemeral routing;
actor-label localization; full negative authorization coverage; fresh independent
security review and live Electron/Web acceptance. No completion or release claim.

Decision replay protection is now implemented for requests carrying a stable
operation ID. The view adapter generates it once per mutation call and preserves
provided IDs; relay correlation uses a separate ID. Runtime events persist the
operation ID and reject payload/actor reuse mismatches. Replaying an older
approval cannot overwrite a later request-changes decision. Regression first
failed with three events/state approved, then passed with two events/state
changes_requested. Mandatory IDs at all lower-level boundaries, merge recovery
parity and full lost-response retry scenarios are still pending.

Merged-source lifecycle guards now reject a new decision against the same clean
source revision without altering the successful merge receipt. A changed source
snapshot starts a fresh decision state and clears stale prepared-merge recovery
fields while retaining events. Real-Git regressions verify both rejection of
reapproval and successful merging of a second source commit without recovering
the first round's merge SHA. Focused race tests passed; unresolved crash-window
recovery requirements above still apply.

Local merge retries now invoke the existing recovery path after validating local
runtime/task provenance. Recovery takes an active-environment reservation and
cross-process environment lock before reading/writing the receipt, as well as
the repository lock. The local real-Git regression simulates a lost final receipt,
rejects recovery while the task environment is active, then recovers the original
merge SHA when idle without another Git merge. Local/remote recovery and decision
replay race tests passed. Ambiguous changed-payload recovery and mandatory
operation IDs still require the remaining acceptance work.

Mutating requests now require a nonblank bounded operation ID at both the cloud
forwarding and daemon HTTP boundaries; reads remain ID-free. Direct API tests
check the specific missing-ID error, and the local endpoint test exercises the
same rejection with otherwise valid task/runtime provenance. Fixtures now assign
distinct IDs to distinct approvals and merges. Focused daemon/handler race tests
passed. The updated process integration also passed on a fresh database without
MR tables: distinct read/approve/merge worker processes completed successfully
with the mandatory operation IDs. The temporary database was removed afterwards.

Merge preparation now persists the original request's actor, comment, snapshot
and operation ID before updating Git. Recovery rejects same-ID payload/actor
changes and preserves the original attribution in the recovered event. Local
recovery derives its local-owner principal before this comparison. The regression
first reproduced acceptance of a changed comment; local/remote recovery, new-round
merge and approval-replay race tests pass after the fix.

Wire MR records now use a dedicated public view containing only snapshot ID,
state, comment, merge SHA and events. Internal prepared-request/commit fields and
the saved recovery snapshot remain on disk rather than being sent alongside the
top-level snapshot. This removes duplicate diff transfer after a merge. The local
endpoint regression first detected a nested recovery snapshot in the response;
local/remote merge, recovery and replay race tests pass with the public view.

Broader regression pass: 14 shared view tests, 9 core transport/diff/inventory
tests, and selected daemon/execenv worktree/reuse race tests passed. The Git engine
package had no matching names in that selected regex; its full suite passed in
the prior explicit package run, not as part of this selection.

Reused-task lookup now falls back to a task-scoped directory binding even when
the repository sits under another managed task root. Successful daemon reuse
writes the new task's directory/runtime bindings in its own claimed root without
overwriting the prior owner's marker. Regression first failed with ownership
rejection, then passed for the current task and still rejected an unrelated task.
Daemon task execution marks both current and prior roots active. Cross-task
alias lifecycle/merge locking and cleanup-manager alias listing need further
acceptance before declaring reused worktrees fully complete.

Reused-source writes now also reserve and lock the actual managed source task
root when it differs from the MR record root. The same protection covers merge
recovery. A regression first reproduced approval despite an active source task,
then passed with busy rejection and idle success. Tests also prove the held
reservation blocks cleanup and a competing environment claim. Focused MR/remote
recovery race tests passed; full alias lifecycle and UI acceptance remain pending.

Cleanup inventory now exposes a valid task-scoped directory binding when the
task root has no checkout of its own, making reused-task MR entries visible.
Runtime provenance also carries business agent ID/name for correct grouping when
completion metadata is absent. Mismatched task/workspace provenance is ignored.
An explicit cleanup test removes only the alias task root, verifies the reused
Git checkout remains intact and the task MR binding is still readable from its
archive. Inventory/reuse/cleanup and prepare-provenance race tests passed. These
filesystem checks do not replace live UI acceptance.

Independent five-lane review completed: overall FAIL. Reports and exact dirty
snapshot hashes are in `.omo/evidence/mr-review-ledger.md`. Review found late
aggregate size enforcement, skipped ordinary receipt archival, and executable
fsmonitor hooks. All three were reproduced by failing tests and fixed. Fresh
code delta review passed; fresh security delta found configured clean filters
still executable during live diff and remains FAIL. QA's isolated browser subset
passed (19 checks/eight screenshots), but real authenticated acceptance is still
unproven. Do not treat these results as completion or deployment approval.

## Historical implementation journal (superseded where noted above)

The entries below preserve earlier implementation and test context. They are not
the current checklist; the current status above takes precedence.

### Architecture change in progress at that point

The database-backed implementation described below predates the user's latest
local-first, relay-only requirement and is NOT the target architecture.
Migrations 455–463, their queries, old HTTP handlers, and frontend adapter still
require replacement/removal. Do not deploy this intermediate
state or claim local-first routing is implemented.

- Runtime decision history now survives snapshot invalidation. A real-Git test
  first failed with an empty history, then passed after adding local events.
- Merge recovery now repairs the authoritative runtime receipt after a Git write
  succeeded without the final receipt being saved. Its regression test first
  failed with state `approved`, then passed with the recovered `merged` state.
- Focused daemon tests passed with race detection and randomized order. These
  checks do not prove server relay or desktop direct access, which remain pending.
- Actor attribution and decision idempotency must be preserved when replacing
  server decisions; never trust a renderer-supplied actor or runtime identity.
- Added `POST /api/local-reviews/execute`: authorizes through existing task/runtime/
  issue metadata and forwards without MR database writes. A bounded in-memory
  exchange is removed on completion/cancellation, with a 45-second request limit.
  Responses use `Cache-Control: no-store` and omit the internal claim token.
- Forwarded actions now include submit, approve, request changes and merge.
  Actor IDs come from authenticated server context, not request bodies. Only
  the runtime owner can merge, and the daemon requires its own matching approval;
  remote merge no longer auto-approves. Regression first failed on the bypass.
- Daemon polling now targets `/local-reviews/relay/claim` and the matching result
  endpoint, returning its local record alongside the Git snapshot.
- Shared MR dialog transport now calls `/execute` for reads and decisions instead
  of create/get/decide cloud MR APIs. Zod parses snapshots, runtime records, and
  history; malformed data fails closed. Four API tests and five view transport/
  dialog tests passed. Views typecheck passed. No authenticated UI acceptance yet.
- Merge recovery responses now include the recovered runtime record, so the
  frontend can parse a recovered response just like a first successful merge.
- Task saved-MR listing, legacy API methods/routes/migrations, and desktop local
  routing are still pending replacement/removal. The snapshot fixture overrides
  the new API method so visual tests cannot accidentally access a real backend.
- Desktop reads now prefer IPC when health identifies the same profile, workspace
  and runtime. Main parses requests and responses, keeps credentials out of the
  renderer, and uses the existing authenticated read-only daemon endpoint. The
  daemon rechecks runtime/workspace membership before resolving task ownership.
  Local errors are surfaced rather than silently rerouted. Local success makes
  no capability/API call to the backend (covered by the routing test).
- Task and shared agent/worktree entries now propagate runtime IDs. The desktop
  cleanup manager's local inventory still lacks runtime IDs and therefore still
  routes remotely; this entrance must be completed. Local mutations remain on
  authenticated server forwarding until local actor/permission handling is wired.
- Routing identity test, three local/remote/error transport tests, related view
  tests and daemon ownership race test passed. No live Electron acceptance yet.
- New task preparation writes `.review-runtime.json` atomically with exact
  workspace/task/runtime identity. Local cleanup inventory exposes runtime ID
  only when the binding matches its authoritative task owner, and the cleanup
  manager now passes it to the MR dialog. Legacy directories without bindings
  continue to use remote access; no runtime is guessed from a provider name.
  Prepare/provenance and matching/stale/wrong-workspace inventory race tests passed;
  desktop typecheck passed. Existing completed-directory backfill and reused-task
  lifecycle coverage still need verification.
- Local decisions now use the same loopback token/profile/no-Origin owner
  authentication as local cleanup, plus exact workspace/runtime membership and
  a matching persisted task runtime binding. Renderer actor IDs are overwritten
  with the explicitly local principal `local-runtime:<id>` (Local runtime owner),
  never attributed to an unverified platform user. Remote forwarding keeps its
  authenticated platform actor. Local approvals/merges no longer call the cloud.
- A real-Git local endpoint test approves and merges through the local handler,
  checks target HEAD and receipt, and verifies spoofed actor IDs are not recorded.
  Four frontend local/remote/error routing tests and desktop typecheck passed.
  Runtime binding is now copied into the task archive along with review receipts.
- Local merge retry/recovery parity, duplicate decision idempotency, negative
  local capability tests, local owner label localization, and independent security
  re-audit remain necessary before release. Prior read-only-loopback audit does
  not cover this newly authorized local mutation path.
- Relay tests cover workspace/runtime isolation, claim tokens, cancellation,
  completed-response release, FIFO and capacity bounds. Handler/server compile
  checks and focused race tests passed. Full authenticated HTTP/DB authorization
  integration for the new forwarding endpoint remains pending.
- Converted the independent-process integration test to the new forwarding
  handlers. It passed on a fresh temporary PostgreSQL database with race detection:
  three worker processes read/approve/merge real Git, target HEAD equals receipt,
  source HEAD remains unchanged, and all three server MR tables contain zero rows
  for the fixture workspace. Authentication is still a fixture; negative production
  authentication cases and frontend integration are not proven by this test.
- The current relay is process-local. Multi-server routing must keep client and
  daemon exchanges on the same API instance or use an ephemeral cross-instance
  transport; this has not been implemented or verified. Do not silently restore
  persistent snapshots/queues to address it.

## Implemented foundations (not an end-to-end completion claim)

- `server/internal/daemon/localreview`: local Git snapshot, bounded diff output,
  tracked/staged/untracked changes, snapshot invalidation, merge preflight and
  clean target update. Tests use disposable real Git repositories.
- The common MR dialog now uses server API transport. Desktop IPC remains as an
  auxiliary local endpoint; user-facing MR operations no longer call it.
- Migrations 455–463: persistent MR snapshots, review events, and runtime-scoped
  command queue. Indexes are concurrent, separate migrations. No foreign keys.
- sqlc queries generated from `pkg/db/queries/local_review.sql`.
- `handler/local_review.go` and `local_review_decision.go` implement creation,
  detail/history and decision transactions. Routes are now registered.
  Read checks runtime visibility and issue access; non-issue runs are restricted;
  merging requires runtime ownership and a matching approved snapshot.
- `local_review_runtime.go` claims runtime commands and validates result claim
  tokens; `daemon/local_review_remote.go` polls and executes on the owning runtime.
- Server worktree inventory query is exposed; agent/task inventory uses it.
- MR creation now upserts by workspace/runtime/task/repository/target identity;
  only the first creation queues the initial read. Frontend no longer searches a
  bounded MR list to identify an existing record.
- Shared repository settings exposes the cross-machine worktree manager, reusing
  the agent Work tab list without its agent filter. Tests cover both entry scopes.
- Database tests verify workspace/runtime isolation, one-time claims, claim-token
  validation and replay rejection. Fresh database migrated successfully then dropped.

## Remaining integration

1. API member/issue/runtime authorization on create/list/read/decision/merge.
   Bind task paths and runtime IDs from authoritative task data, not client input.
   Create/get/list/decision routes and busy-command checks exist. Integration tests
   must still prove authorization and the complete remote round trip.
2. Queue reads/merges transactionally with expected snapshot and actor identity.
   Authorize merges against runtime ownership; preserve task visibility rules.
3. Current command consumer polls every five seconds. Integrate optional hints
   and capability/version gating to avoid repeated calls against older servers.
4. Persist daemon results and review events transactionally. Uncertain merges must
   be reconciled rather than automatically retried. Preserve last snapshot offline.
5. Frontend MR adapter and task/agent inventory now use server queries. Worktree
   management now has cross-machine inventory in shared repository settings.
   Review integration with the original desktop cleanup manager and add paging to
   the 200-row remote inventory. Stable MR identity is implemented and DB-tested.
6. Finish multiple-repository and finalized/local-directory source resolution.
   MR identity and audit history must survive snapshot refresh and runtime restart.
7. Protect target worktrees used by active tasks and external checkout races;
   review changed Git state again immediately before writing target refs.
8. Add transport, permissions, offline, stale-snapshot, approval/merge and three-entry
   end-to-end tests. Capture actual UI. No production merge has been performed.

## Latest verification

- Go race tests for Git snapshots/merge and daemon review handler passed.
- sqlc generation passed.
- All migrations applied in `multica_mr_migration_check_20260908`; test database removed.
- `TestServerQueueIsRuntimeScopedAndClaimsCannotBeReplayed` passed on PostgreSQL.
- Views typecheck passed after fixing test role queries.
- Handler package compile check (`go test ./internal/handler -run '^$'`) passed.
- Server, handler and daemon compile checks passed after route registration.
- Views typecheck passed after switching the dialog to server requests.
- Deduplication migration and duplicate-create behavior passed on a fresh
  `multica_mr_dedupe_check_20260908` PostgreSQL database; database removed afterward.
- Shared worktree list tests cover agent filtering, multiple runtimes and matching
  task/path selection. These are component tests, not a live remote acceptance run.
- `TestLocalReviewAPIRoundTrip` passed with PostgreSQL: create/upsert, claim,
  report, duplicate acknowledgement, stale/unapproved rejection, approval,
  queued merge, frozen decisions and persisted merge result. Its runtime reply
  is a fixture, not an actual remote process. Temporary database removed.
- `TestRemoteReviewCommandReadsAndMergesOnOwningRuntime` passed with real Git:
  command execution produces diff, merges the owning target checkout, preserves
  source HEAD, and rejects a foreign runtime. These complement the API test but
  do not replace a single end-to-end multi-process acceptance run.
- Untracked-file reads are now bounded through `os.OpenRoot` and validate file
  identity after opening. Target merges explicitly disable Git auto-stash.
- Merge preparation now fsyncs a record of the exact planned commit and snapshot
  before changing refs. Re-delivered commands inspect that record under the
  repository lock and return the original commit when it is already on target.
- Server can re-deliver a running command after two minutes without changing its
  claim token. This still needs transport/restart integration coverage. A prepared
  commit absent from target is reported explicitly and is not blindly re-applied.
- Tests verify duplicate merge delivery preserves the same target HEAD and a
  journal-write failure prevents any target-ref update.
- Git patch viewer now shows actual old/new file line numbers. Binary digests
  remain non-source metadata; tests cover hunk resets and untracked text.
- A saved MR can be opened by its server ID even after its task worktree was
  finalized. End-to-end finalized-directory recovery is still pending.
- Isolated real-component browser fixture: `scripts/qa/local-mr/` (Vite port 5189).
  Screenshots `/tmp/multica-mr-visual.12H30I/final-{375,768,1280}.png` captured in
  Chromium; no page errors, no dialog horizontal overflow. Independent visual
  review found no blocking CJK/layout issues in those three resting states.
  This is NOT a remote-runtime acceptance test. Confirmation was exercised on
  the fixture API only; no actual repository was merged by this browser fixture.
- Container workdirs now discover bounded nested Git repositories. Single-repo
  containers resolve automatically; multiple repositories return a selection list.
  No approval is accepted for a container without a selected repository HEAD.
  Task subdirectories normalize to a Git root only inside the owned environment.
- New MR tables are explicitly deleted with workspace PR data, and included in
  the workspace deletion manifest. Manifest also names existing notification
  tables whose deletion was already implemented. SQL generation passed; database
  deletion behavior still needs the workspace teardown integration tests.
- Prepare now stamps external in-place directories into `.review-directory.json`
  under the daemon-owned task environment. Review resolution validates both that
  binding and the task owner; arbitrary external paths remain rejected. Binding
  tests cover correct, wrong-task and wrong-directory cases. Older tasks without
  bindings and finalized worktrees still need lifecycle reconciliation.
- Source-equals-target snapshots can display in-place uncommitted changes, while
  merging a branch into itself is rejected and disabled in the dialog.
- Merge guards share local-directory task bookkeeping: overlapping active paths
  block the operation, unrelated tasks do not. New task activation synchronizes
  on that guard. Race tests for locks and remote merge execution passed.
- New local-directory worktree finalization records the delivered commit and
  repository before removing the temporary checkout. ReadCommitted compares
  that pinned commit independently of the user's later index/untracked edits.
  Tests cover removed-worktree lookup, wrong-task rejection, stable committed
  diff and dirty-target merge refusal.
- Before task environment deletion, GC archives delivery bindings and merge
  recovery receipts under `.local-mr-archive/<workspace+task hash>`. Writes are
  bounded to the daemon workspace root and synced before deletion. Archive failure
  preserves the task directory. Review lookup falls back to the scoped archive.
  A real-Git test now deletes the entire environment and verifies the same pinned
  snapshot remains readable. Missing legacy provenance is still reported unavailable.
- Workspace deletion manifest and MR API round-trip tests passed on a fresh
  PostgreSQL database after all migrations. Database removed after verification.
- Local dialog flow, task entry, and agent tab baseline tests passed.
- Desktop worktree-manager baseline tests passed. Three-entry MR-specific coverage
  and visual acceptance remain incomplete.

Do not mark the user objective complete from these foundation checks.

## Final audit blockers and deployment observation

- Independent code audit: `.omo/evidence/local_mr_final_code_audit-code-review.md`.
  Checked-out target final ref write needs expected-HEAD enforcement against
  external Git operations; prepared-but-unapplied merges must be recoverable.
  Direct desktop merge IPC should be removed/restricted in favor of server audit.
- Multi-process HTTP/DB/runtime test passed using real Git and two independently
  launched worker subprocesses. Authentication is a fixture; authorization has
  separate handler tests. Temporary database removed.
- Observed production `/api/config` still reports `sha-922a6a8...` without local
  MR capability. User's loading screenshot cannot be treated as new server deployed.
  Added explicit `local_review_supported` capability, old-server message and bounded
  MR HTTP requests. No MR code has been deployed in this implementation run.
- Target input now applies only on explicit comparison. Fixed its test's
  combobox role and reran the actual package Vitest setup successfully.

## Merge re-audit

- `.omo/evidence/local_mr_merge_reaudit-code-review.md`: APPROVE, no blockers.
- Checked-out targets now use a local file-transport Git receive-pack transaction
  with updateInstead and an exact old-ref lease. The planned commit's first parent
  must equal the approved target. Tests rewind the target after preflight and
  verify both target ref and files remain unchanged on rejection.
- Failed preparation can be retried only by a fresh command ID when the target
  still equals its approved commit. Same-command redelivery never repeats a write.
- Removed desktop review mutation IPC. Loopback HTTP review endpoint is read-only;
  decisions/merges are accepted only from the authenticated platform command path.
- Re-ran HTTP/PostgreSQL/independent-worker process integration after these fixes:
  PASS, platform merge SHA equals target HEAD and source HEAD is preserved.
  Temporary database removed. Desktop typecheck and diff whitespace checks passed.
