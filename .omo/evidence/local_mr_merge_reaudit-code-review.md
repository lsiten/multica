# Local MR merge re-audit

## Scope and evidence

Read-only re-audit of the prior blockers in
`.omo/evidence/local_mr_final_code_audit-code-review.md`, against the current
implementation in:

- `server/internal/daemon/localreview/merge.go`
- `server/internal/daemon/localreview/merge_test.go`
- `server/internal/daemon/localreview/record.go`
- `server/internal/daemon/local_review_recovery.go`
- `server/internal/daemon/local_review_remote.go`
- `server/internal/daemon/local_review_remote_test.go`
- `server/internal/daemon/worktree_review.go`

`omo ulw-loop status --json` could not resolve an attempt directory because
the installed OMO launcher points at a missing runtime bundle. This report
therefore uses the required fallback path. The reviewed local-review files are
currently untracked, so this assessment is grounded in the current source,
the prior report, and independently executed tests rather than a Git patch for
those files.

The CodeGraph tools were not available on this review surface, so source and
call-path checks used targeted `rg` and direct file inspection.

## Findings

### CRITICAL

None.

### HIGH

None. Both former HIGH blockers are resolved.

1. **Checked-out-target compare-and-swap race — resolved.**
   `MergePrepared` still verifies the checked-out target is clean, at the
   approved `HEAD`, and on the target branch before a write
   (`merge.go:65-80`). `updateCheckedOutTarget` additionally proves the
   generated merge commit's first parent is the approved target
   (`merge.go:84-90`) and pushes through a local `file://` receive-pack with
   `receive.denyCurrentBranch=updateInstead` plus an explicit
   `--force-with-lease=<target>:<approved-old-sha>` (`merge.go:91-100`). The
   receiver therefore atomically rejects a target ref that moved after the
   preflight check, rather than letting `merge --ff-only` consume a rewound
   target.

   `TestCheckedOutTargetRejectsRewindAfterValidation`
   (`merge_test.go:27-55`) changes the real target ref after merge preparation
   and proves the leased update fails, leaves `HEAD` at the external value,
   and leaves the target checkout clean.

2. **Prepared-but-unapplied receipt could not be retried — resolved.** When a
   receipt exists but its prepared commit is absent from the target,
   `recoverRemoteMerge` permits only a nonempty, different command ID and only
   if the recorded target ref still equals the approved target SHA
   (`local_review_recovery.go:36-46`). It clears the stale preparation and
   resumes the normal guarded approval/merge flow. The same command instead
   returns an error without re-executing the write (`local_review_recovery.go:47-48`).

   `TestRemoteMergeCanRetryFixedPreconditionWithNewCommand`
   (`local_review_remote_test.go:66-104`) uses real temporary Git worktrees:
   it makes the checked-out target dirty after receipt preparation, cleans it,
   verifies redelivery of `merge-first` remains rejected, then verifies a
   distinct `merge-retry` ID merges successfully.

3. **Privileged desktop bypass / writable loopback endpoint — resolved.** No
   desktop `daemon:review-worktree`, `reviewWorktree`, or equivalent bridge
   remains under `apps/desktop`. The public daemon loopback route calls
   `worktreeReviewHandler`, which fixes `allowDecisions` to `false`
   (`worktree_review.go:115-117`); any action other than read is rejected
   (`worktree_review.go:135-137`). The daemon's authenticated queue consumer
   is the sole code path that invokes the internal decision handler with
   `allowDecisions=true` (`local_review_remote.go:39-57`).

   `TestLocalReviewLoopbackEndpointCannotApproveOrMerge`
   (`worktree_review_test.go:109-125`) confirms `submit`, `approve`,
   `request_changes`, and `merge` receive HTTP 403 through the public loopback
   handler.

### MEDIUM

1. **`worktree_review.go` exceeds the repository's source-size ceiling.**
   `server/internal/daemon/worktree_review.go` is 253 pure lines (measured as
   non-blank, non-comment lines), above the 250-line maximum. It currently
   owns review-root binding, loopback authorization, environment/repository
   locking, snapshot and record handling, and merge dispatch
   (`worktree_review.go:35-263`). This is a maintainability concern, not a
   demonstrated merge or recovery failure. Before this feature grows again,
   split one cohesive responsibility (for example, review-root resolution or
   request-operation dispatch) into a named file with focused tests.

### LOW

None.

## Test and verification assessment

Independently executed, all passing:

- `cd server && go test -race ./internal/daemon/localreview -run '^(TestCheckedOutTargetRejectsRewindAfterValidation|TestMergeUpdatesTargetCheckoutAndPreservesSource|TestMergeRejectsStaleAndDirtySnapshots)$' -count=1 -v`
- `cd server && go test -race ./internal/daemon -run '^(TestRemoteReviewCommandReadsAndMergesOnOwningRuntime|TestRemoteMergeCanRetryFixedPreconditionWithNewCommand)$' -count=1 -v`
- `cd server && go test -race ./internal/daemon -run '^TestLocalReviewLoopbackEndpointCannotApproveOrMerge$' -count=1 -v`
- `cd server && go vet ./internal/daemon/localreview ./internal/daemon`

The tests use `t.TempDir()` Git repositories/worktrees; no user repository or
agent CLI was exercised. They cover the requested wrong-target-HEAD, dirty
target, fresh-command retry, same-command redelivery, and read-only loopback
cases. The optional cross-process HTTP/PostgreSQL test was not run because its
required test database environment variable was not supplied; this is an
explicit coverage gap, not a passing claim.

## Skill-perspective check

Ran: **yes**. I fully consulted `omo:remove-ai-slops` and `omo:programming`,
including the programming skill's Go guidance, before judging tests and
maintainability.

- **remove-ai-slops:** No deletion-only test, test that merely proves a
  requested removal, tautological assertion, implementation-constant mirror,
  or needless production parsing/normalization was found in this scope. The
  real-Git tests assert observable ref/tree and command outcomes.
- **programming:** No untyped escape hatch, prompt-text test, or needless
  production abstraction was found. `updateCheckedOutTarget` is a justified
  private test seam for the atomic write, rather than speculative indirection.
  The 253-pure-LOC `worktree_review.go` violates the size perspective and is
  recorded as MEDIUM.

## Outcome

- `codeQualityStatus`: **WATCH**
- `recommendation`: **APPROVE**
- `reportPath`: `.omo/evidence/local_mr_merge_reaudit-code-review.md`
- `blockers`: **None.**

The former merge/recovery blockers are resolved. The MEDIUM size finding
should be addressed before adding further responsibility to
`worktree_review.go`; it does not warrant blocking this focused fix.
