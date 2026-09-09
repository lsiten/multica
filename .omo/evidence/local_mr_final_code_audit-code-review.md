# Local MR code review

## Review scope

Read-only review of the uncommitted local-review implementation, concentrating on
the runtime command queue, daemon-owned Git operations and recovery, SQL state
transitions, and the shared web/desktop entry points.

The requested `omo ulw-loop status --json` command could not run because the
installed OMO runtime points at a missing `4.19.2` CLI bundle.  No attempt
directory could therefore be resolved; this is the required fallback artifact.

## Findings

### CRITICAL

None.

### HIGH

1. **A checked-out target branch can change after validation and still be
   merged.**  In [merge.go](../../server/internal/daemon/localreview/merge.go#L62),
   the code reads and compares the target worktree's `HEAD` to the reviewed
   `current.TargetHead`; it then runs `git merge --ff-only` at
   [line 74](../../server/internal/daemon/localreview/merge.go#L74).  That final
   command has no expected-old-ref compare-and-swap.  A user/external Git
   process can reset the checked-out target to an ancestor after line 66 and
   before line 74.  The reviewed merge commit is then a descendant of the
   newly-reset target, so `--ff-only` succeeds and silently overwrites the
   intervening target decision.  The un-checked-out path correctly uses
   `update-ref <new> <expected-old>` at lines 58-60, demonstrating the missing
   guard is limited to the checked-out path.  This violates the required merge
   race guarantee.

   Required correction: retain an atomic expected-old update semantics when a
   target worktree is present, and update the worktree only after that guard;
   add a deterministic test that changes the target ref between validation and
   the final write and proves the merge is rejected.

2. **A prepared-but-unapplied remote merge is permanently unretryable.**
   `MergePrepared` writes the recovery receipt before touching the target
   ([merge.go](../../server/internal/daemon/localreview/merge.go#L49-L53)); a
   later target-worktree dirty/changed check can fail at
   [lines 62-74](../../server/internal/daemon/localreview/merge.go#L62-L74).
   On every later remote command, `runRemoteReview` first calls
   `recoverRemoteMerge` ([local_review_remote.go](../../server/internal/daemon/local_review_remote.go#L38-L41)).
   When the receipt is for the same snapshot but its commit is not on the
   target, recovery sets an error and returns `ok=true`
   ([local_review_recovery.go](../../server/internal/daemon/local_review_recovery.go#L33-L38)).
   That prevents the normal merge path from running again.  Cleaning the
   target worktree and refreshing does not change a committed snapshot ID, so
   the UI's retry is repeatedly short-circuited instead of recovering.

   Required correction: make the journal state explicit and recoverable--for
   example, distinguish “prepared, not applied” from “applied” and permit a
   guarded resume/rebuild after revalidation--rather than treating it as a
   terminal error.  Add a real-Git regression where the target checkout is
   dirty after the receipt write, is cleaned, and the same approved snapshot
   subsequently merges once.

### MEDIUM

1. **The new desktop IPC exposes an ungoverned second merge path, but no
   product entry point uses it.**
   [daemon-manager.ts](../../apps/desktop/src/main/daemon-manager.ts#L1411-L1417)
   proxies `daemon:review-worktree` directly to the local daemon, and the
   preload exposes it at
   [index.ts](../../apps/desktop/src/preload/index.ts#L272-L273).  The local
   handler's decisions mutate only its local `Record`
   ([worktree_review.go](../../server/internal/daemon/worktree_review.go#L184-L241));
   they do not go through the central local-review command/event state used by
   web and desktop clients.  A source search found no renderer invocation of
   `reviewWorktree`, while the worktree manager uses `LocalReviewDialog`, which
   uses the central API.  This is needless privileged production surface and
   creates a future route that can bypass the owning-runtime queue/audit model.
   Remove it unless it is deliberately made the one authoritative operation
   path with equivalent server state and authorization.

2. **The new target-edit regression test addresses the right behavior but is
   currently written against the wrong accessible role.**
   [local-review-dialog.test.tsx](../../packages/views/issues/components/local-review-dialog.test.tsx#L28)
   asks for a `textbox`; the rendered `<input list="local-review-branches">`
   at [local-review-dialog.tsx](../../packages/views/issues/components/local-review-dialog.tsx#L46)
   has the `combobox` role.  The test therefore fails before exercising the
   no-dispatch assertion.  Use the actual accessible role (or query by label)
   and run it through `packages/views`' Vitest configuration.  This is a
   brittle test defect, not evidence that the target behavior itself is wrong.

### LOW

None.  The reviewed production sources measured 9–244 pure LOC, so none
exceeded the imported skill's 250 pure-LOC ceiling.

## Test and evidence assessment

The reported real HTTP/PostgreSQL/two-process scenario is valuable and covers
the success path from queue to owning-runtime read/merge.  It does **not**
cover either HIGH state: external target movement in the post-check write
window, or the receipt-written / target-apply-failed retry path.  Existing
`TestRemoteReviewCommandReadsAndMergesOnOwningRuntime` only verifies a receipt
after a successful merge acknowledgement; `TestMergeUpdatesTargetCheckoutAndPreservesSource`
only covers an unchanged checked-out target.

I independently ran:

- `go test ./internal/daemon/... -run '^(TestLocalReview|TestRemoteReview|TestMerge|TestReviewPathGuard)' -count=1` — PASS.
- `go test ./internal/handler -run '^TestLocalReview' -count=1` — PASS.

An initial root-level Vitest invocation did not load the views package setup
file, so its matcher failures are not treated as product evidence.  The
target-edit test has a separate role mismatch as noted above.

## Skill-perspective check

Ran: **yes**.  I consulted `omo:remove-ai-slops` and `omo:programming`, plus
their Go and TypeScript routing references, before judging tests and
maintainability.

- `remove-ai-slops`: no deletion-only, prose-pinning, or implementation-
  constant-mirroring tests found in the reviewed local-review tests.  The
  unused desktop IPC is unnecessary production capability/data path and is
  reported as MEDIUM.
- `programming`: the diff violates the perspective through that redundant
  privileged path and the brittle incorrect-role test.  The HIGH findings are
  correctness failures, independent of the style perspective.

## Outcome

`codeQualityStatus`: **BLOCK**

`recommendation`: **REQUEST_CHANGES**

`blockers`:

1. Close the checked-out-target compare-and-swap race.
2. Make a prepared-but-unapplied remote merge recoverable/retryable.
