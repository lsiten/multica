# Local MR code review

Verdict: FAIL — two major correctness/resource blockers below. Review is read-only apart from this evidence file. No agent CLI, deployment, or commit was run.

## Major blockers

1. **Live snapshot memory is bounded only after allocation.** `server/internal/daemon/localreview/snapshot.go` appends every tracked patch (around line 154) and every untracked file body (around line 202), then checks the total only in the final hashing loop (around lines 208–212). Each file may independently approach 8 MiB. A generated directory containing hundreds of individually permitted text files therefore retains hundreds of MiB or more before returning `ErrTooLarge`; the global `localReviewOperations` mutex is held during this work, blocking all review requests. This is a source-proven bound violation, not an observed OOM. Enforce aggregate remaining budget before retaining each patch/body, bound entry count, and test early termination across several individually valid files. `ReadCommitted` already checks incrementally.

2. **Fresh managed-worktree review receipts are silently deleted by GC.** `server/internal/daemon/execenv/review_archive.go:22–24` treats absence of `.review-directory.json` as a successful no-op. Fresh managed environments write `.review-runtime.json` but only `env.LocalDirectory` writes the directory binding (`execenv.go`, final preparation block); reused environments explicitly write it in `daemon.go`. Normal managed review decisions save `.local-review-<key>.json` directly in the task root. `gc.go:895–900` then calls this no-op archive function and removes that whole root. Consequently the supposedly runtime-owned durable MR event history/recovery receipts of normal managed worktrees disappear on cleanup. Preserve receipts independently of external-directory bindings, or establish an appropriate durable binding/snapshot for all reviewed task kinds before cleanup. Existing archive coverage exercises aliases/external pinned deliveries, not fresh managed receipt survival.

## Verification and coverage

Ran from `server/`:

```sh
../scripts/go-test-with-agent-cli-guard.sh go test ./internal/daemon/localreview ./internal/daemon ./internal/handler -run 'Test(LocalReview|WorktreeReview|Read|Merge)' -count=1
```

Result: all three packages passed (`localreview` 2.245s, `daemon` 8.257s, `handler` 0.472s); no guarded ambient agent invocation. This does not prove DB-backed cases ran if their own fixtures skip for unavailable services.

Reviewed runtime snapshot/merge/receipt/recovery, external/reused bindings, source/path locks, relay authorization/capacity, inventory SQL, response schemas, dialog/platform routing, and desktop IPC. CAS target ref update, clean checked-out target validation, immutable committed snapshots, merge preparation receipts, command payload checks, and finite relay capacity are present. No concrete target-history rewrite defect was found in inspected paths.

Read root CLAUDE.md and global coding/Go/Node/RTK instructions. No project-local `.codex/rules/` directory exists. CodeGraph status tools are unavailable on this subagent surface, so exact source reads and bounded text search were used. Current source was authoritative; historical implementation notes were not used as proof. The workspace is concurrently edited, so line numbers and findings must be rechecked against subsequent fixes. No browser/Electron acceptance, full typecheck, full test suite, crash/power-loss fault injection, or actual OOM experiment was performed.
