# Local MR code delta re-review

Verdict: PASS for the two assigned prior blockers. No remaining blocker found in this delta. This is not full-product approval.

Reviewed current dirty source on HEAD `9da4ed8163a377e59b6a74d13d89c5599f277093`. All four assigned source/test files are currently untracked. Inspected the complete `Read`, `ArchiveReviewDirectory`, `isReviewReceipt`, both new regression tests, metadata readers, archive resolution, GC call site, and relevant existing daemon tests. Read root CLAUDE.md and global RTK/coding/Go rules; no project-local `.codex/rules/` or nested server AGENTS.md was found. CodeGraph exploration was available but did not locate the new untracked archive symbols; current source reads supplied that delta.

## Prior blockers

1. **Aggregate retained patch budget: resolved.** `server/internal/daemon/localreview/snapshot.go:146–160` introduces one running total and rejects a tracked patch before appending it. Lines 207–211 apply the same budget to untracked patches. The total cannot exceed 8 MiB, and the over-budget file is not retained in `Snapshot.Files`. `TestReadStopsAtAggregateBudgetBeforeLaterFiles` creates two individually permitted text files exceeding the aggregate limit and a lexically later invalid symlink; its `errors.Is(ErrTooLarge)` assertion distinguishes early termination from the former late check. Transient per-file reads and Git output remain separately bounded; this is not a claim that the process RSS or complete serialized response fits 8 MiB. No explicit file-count cap was added; the original accumulating-many-large-patches defect is resolved.

2. **Ordinary managed receipts lost during GC: resolved for preservation.** `server/internal/daemon/execenv/review_archive.go:22–48` now looks for regular, correctly named receipt files even without a directory binding, requires authoritative workspace/task ownership, and derives the archive key from that owner. Lines 77–99 preserve and validate optional directory/runtime metadata; lines 106–125 copy bounded receipt content. The no-binding/no-receipt case still returns without creating an archive. `TestArchiveReviewReceiptsWithoutDirectoryBinding` proves byte-for-byte preservation for this former no-op path. `gc.go:895–903` refuses deletion on archive failure and deletes only after success. Existing pinned-delivery and alias-cleanup tests also pass.

## Verification

Ran from `server/`:

```sh
../scripts/go-test-with-agent-cli-guard.sh go test ./internal/daemon/localreview ./internal/daemon/execenv ./internal/daemon -run 'Test(Read|ArchiveReview|WorktreeReview|LocalReview)' -count=1
```

Exit 0: localreview 1.603s, execenv 0.312s, daemon 8.145s. No ambient agent CLI invocation was reported by the guard. No real user agent CLI, external write, source edit, or secret/config read was performed. Only this requested evidence file was written.

## Limits

Ordinary managed receipt preservation does not itself make a deleted checkout browsable: `worktree_review.go:103` still requires an archived directory binding for resolving a missing path. The new receipt unit test proves archival bytes, not end-to-end ordinary managed post-GC HTTP history/recovery. Do not extend this PASS into that claim. No OOM experiment, crash/power-loss fault injection, race run, browser/Electron acceptance, or full test suite was performed. The parent must retain any wider product gates independently.
