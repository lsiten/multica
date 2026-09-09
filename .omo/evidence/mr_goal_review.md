# Local MR goal and constraint review

Verdict: **FAIL for unconditional completion of the stated goal; PASS for the inspected runtime-owned architecture and three source-wired entrances.**

Reviewed dirty tracked and untracked implementation against HEAD `9da4ed8163a377e59b6a74d13d89c5599f277093`. Read CLAUDE.md, global coding/Go/Node rules and RTK instructions. CodeGraph exposed only its exploration tool; the attempted status-shaped request was rejected, while a query succeeded against the existing graph. Exact local source was used for the new untracked MR files. No source edits, real agent CLI, external writes, production mutations, or user Git operations were performed. This evidence file is the only intended repository write.

## Exact goal gap

1. **Owner-machine direct routing is implemented for Electron only, not an ordinary Web browser on that same machine.** `packages/views/platform/local-review.ts:9-20` tries only `window.daemonAPI.readLocalReview`; without that preload bridge it calls the server capability and relay APIs. The bridge is installed in desktop, and `apps/desktop/src/main/daemon-manager.ts:1412-1419` selects direct access only after matching local profile/workspace/runtime. There is no Web local-daemon discovery/connection path in this adapter. This is a blocker under the supplied literal requirement that an owning-machine client uses its daemon directly; if the intended requirement is specifically owning-machine **Desktop**, this item becomes a scope clarification, not a defect. The implementation status document itself currently says “Any authenticated Web or Desktop client” and “An owning-machine client accesses its daemon directly” (`LOCAL_MR_IMPLEMENTATION.md:5-7`). Do not silently equate those scopes.

2. **Acceptance remains partial.** This review verified source flows and an isolated real-Git package suite, not authenticated Web/Electron operation or a deployed single-server Docker round trip. Source wiring and targeted test passes cannot substantiate completion of those user surfaces. This is an evidence gap, not evidence that those flows fail. The parent review may satisfy this with independently captured acceptance evidence.

## Achieved constraints

- Runtime owns MR decisions/history/receipts: `server/internal/daemon/localreview/record.go:13-35,57-102` loads and atomically replaces `.local-review-<key>.json` inside the task-owned runtime root. The record key includes repository path and target (`:52-54`). Public record views exclude the private recovery snapshot (`:38-49`).
- Server does not persist MR snapshots/events/durable commands in the inspected path. `server/internal/handler/local_review_forward.go:70-124` authorizes using existing task/runtime metadata and enqueues an active exchange. `local_review_relay.go:18-29,48-59` retains exchanges only in process memory and deletes them on completion/cancellation. `server/pkg/db/queries/local_review_worktrees.sql:1-12` is a SELECT over existing task, agent and runtime records. Temporary forwarding memory is consistent with the prohibition on **persisting** an MR queue.
- Local Git MR, no GitHub PR flow: `server/internal/daemon/localreview/snapshot.go:75-217` reads local refs/diffs; `merge.go:42-99` computes merge objects and updates local refs/checkouts, including a `file:` receive-pack for a checked-out target. This does not contact GitHub.
- Explicit merge is separate from submit/approve/request-changes. `worktree_review.go:294-305` implements the decisions and requires approved state before merge; `packages/views/issues/components/local-review-dialog.tsx:79-91` exposes separate actions and a branch/SHA confirmation step.
- A single server instance is compatible with the in-memory relay. Arbitrary multi-instance load balancing would need affinity/shared ephemeral routing, but that is **not** treated as a blocker: distributed deployment was not explicitly requested, and adding database MR persistence would violate the updated requirement.

## Actual source flow traces

1. **Task worktree → local desktop operation:** `code-review-context-section.tsx:19-36` lists task attempts, selects repository/work directory, opens a request carrying task/workspace/runtime identity → `local-review-dialog.tsx:23-35` loads/executes → `platform/local-review.ts:9-16` invokes preload → `daemon-manager.ts:1412-1419` validates ownership → `worktree_review.go:130-208` validates local credential/profile/runtime/task binding → runtime snapshot/record handling.
2. **Agent Work tab → remote operation:** `packages/views/agents/components/tabs/activity-tab.tsx:177` mounts `LocalWorktreeReviews` → `local-worktree-reviews.tsx:19-38` uses workspace/agent-filtered paginated inventory and opens the same dialog → `platform/local-review.ts:18-20` → `packages/core/api/client.ts:797-803` → `ForwardLocalReview` → ephemeral exchange → `daemon/local_review_remote.go:104-128` claims with owning runtime, runs `reviewOperationHandler(true)` (`:49-80`) and posts result. The server forwards only while the caller waits.
3. **Worktree manager → direct operation:** `apps/desktop/src/renderer/src/components/worktree-manager.tsx:129-138` selects a repository from daemon worktree inventory and opens the same request/dialog. Desktop routing checks actual runtime ownership rather than assuming every desktop request is local. A non-owned runtime falls back to the backend relay.
4. **Approve → explicit merge:** mutation carries the loaded snapshot ID (`local-review-dialog.tsx:30-35`); daemon reloads snapshot and rejects changes (`worktree_review.go:281-284`), verifies approval (`:300-305`), saves the prepared receipt before changing refs (`:325-337`), merges locally, and saves final state/history. `merge.go:30-40,65-80` protects stale/dirty/same-branch/empty/changed-target cases.

## Edge cases reviewed

| Case | Source evidence | Result |
| --- | --- | --- |
| Dirty/staged/untracked source | `snapshot.go:133-202`; `merge.go:33-35` | Included in diff; merge refuses dirty source. |
| Source or target changes after approval | `snapshot.go:204-216`; `worktree_review.go:281-284`; `merge.go:30-32` | Snapshot fingerprint invalidates approval. |
| Dirty checked-out target | `merge.go:65-75` | Refuses target update; source untouched. |
| Conflicting branches | `merge.go:42-45` | Computes in object DB; refuses without checking out/merging either working tree. |
| Runtime offline / late result | `local_review_forward.go:105-125`; `local_review_relay.go:48-59,82-87` | Times out; no stale cloud snapshot; late result rejected. |
| Replayed mutation ID | `worktree_review.go:259-274` | Matching event replays read; changed action/snapshot/comment/actor rejects. UI generates a fresh ID per new mutation call, so transparent lost-response retry across user clicks is not implied. |
| Active task or concurrent workspace reuse | `worktree_review.go:210-242` | Runtime/environment/source/repository locks refuse mutation while busy. |
| Multiple repositories | `snapshot.go:82-97`; `local-review-dialog.tsx:63` | Returns selection inventory instead of choosing among multiple repositories. |
| No local main branch | `snapshot.go:103-120`; dialog `:16-17,53-55` | Initial default main fails visibly; user can type another target, but branch suggestions are unavailable until a successful snapshot. UX limitation, not an invented requirement blocker. |
| Already-merged revision | `worktree_review.go:277-284` | Rejects another decision for the same clean source; preserves receipt. |

## Verification run in this review

`cd server && go test ./internal/daemon/localreview -count=1` → **PASS**, `ok .../localreview 2.056s`.

The suite includes real temporary-repository checks for committed/staged/untracked diff preservation, invalid target/symlink rejection, multi-repository discovery, journal failure before ref write, target rewind rejection, source preservation, stale/dirty snapshot rejection, conflict preservation, and committed review isolation from later directory edits. This does not run authenticated UI, production Docker, daemon account integration, or real agent CLIs. Existing implementation journal claims were not counted as fresh acceptance evidence.
