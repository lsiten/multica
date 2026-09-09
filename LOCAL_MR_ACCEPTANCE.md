# Local MR acceptance matrix

This is the current requirement checklist. `LOCAL_MR_IMPLEMENTATION.md` contains
chronological history and may describe superseded intermediate states.

| User requirement | Current evidence | Status |
| --- | --- | --- |
| Task-associated branch/worktree MR entrance | Real dialog mounted by task-entry integration test; selected task/runtime/path and approval checked | Verified at component boundary |
| Agent Work tab associated worktrees | Production tab imports shared worktree view; real-dialog tests filter agent and show selected diff | Source + component verified |
| Worktree-management entrance | Shared and desktop manager real-dialog tests; review does not invoke cleanup | Component verified |
| View actual commits/files/diffs | Real temporary Git tests for committed, staged, untracked, binary and finalized delivery; browser dialog captures | Verified in isolated integration |
| Submit/approve/request changes/confirm merge | Runtime records and replay tests; real dialog two-step confirmation; independent worker merge matches target HEAD | Verified in isolated integration |
| Execute Git on owning runtime | JWT-authenticated HTTP/database/independent-process test and daemon task/runtime ownership tests | Verified in isolated integration |
| Owning desktop runtime bypasses server for Git | Real Electron/preload/IPC fixture plus local HTTP handler; shared routing tests assert no backend call | Verified for supported desktop route |
| Other runtime uses server forwarding | Production Auth + membership/human guards, ephemeral relay, owning worker process tests | Verified on single-server test setup |
| Server avoids MR persistence | No MR migrations/models/mutations; fresh DB has no MR tables after read/approve/merge | Verified |
| Legacy local worktrees | Owner-only metadata discovery and one-time local binding verification; identity mismatch rejection; subsequent local calls retain runtime | Verified in component/HTTP/DB tests |
| Safe reuse/cleanup/recovery | Actual source-root locks, alias cleanup retains checkout, receipt archival, CAS target update, lost receipt and replay tests | Verified in isolated Git/filesystem tests |

## Remaining release evidence / decisions

- Same-computer ordinary Web direct access is not implemented. A normal Web client
  has no attached Electron daemonAPI and uses forwarding. A focused user scope
  question is pending; do not silently claim this route is direct.
- A production-account sign-in and navigation recording is not available. The
  tests use isolated identities and environments, including production JWT
  verification; do not present them as the user's deployed service being tested.
- Later legacy-discovery/main-profile security review passed. Its code review
  found target-dependent runtime cache loss; the fix now has independent PASS
  and mounted target/repository switching regressions. These remain scoped
  reviews, not blanket approval of untested deployed-user navigation.
- No release, package, Git commit, push, or production deployment is part of the
  current verified result. Desktop production-mode compilation has passed in an
  isolated directory; that output is not an installer with a released daemon.

## Explicit boundaries

- Initial legacy metadata discovery/verification may contact the server. It reads
  existing authorized identity metadata, not Git data or cloud MR snapshots.
- Filtered files are reviewed as raw working content with command-producing Git
  filters disabled. LFS/custom conversion equivalence is not claimed.
- Relay is process-local for the single-server deployment under test. Arbitrary
  distributed API-node routing was not explicitly requested and is not verified.
- Discarding a checkout does not guarantee its historical code stays browsable;
  saved local receipts are retained. Availability of code still depends on the
  owning runtime/repository, not a cloud snapshot fallback.

Latest focused rerun: 14 shared-view tests, 9 desktop tests and selected Go
daemon/execenv/localreview MR/worktree/recovery race tests passed. This matrix
does not mark the overall goal complete while the remaining items are unresolved.
