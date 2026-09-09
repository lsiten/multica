# Local MR hands-on QA — FAIL full acceptance; PASS executed subset

Date: 2026-09-08, approximately 22:40–22:45 Asia/Shanghai. HEAD: `9da4ed8163a377e59b6a74d13d89c5599f277093`, with the existing dirty tree. No production source changes, real task repository writes, authenticated browser sessions, user daemon restarts, or installed agent CLI execution were performed.

The executed subset passed: 19 real-browser checks, 22 focused Vitest tests, 14 top-level daemon tests (plus three inventory subcases), and nine Git review tests. Full acceptance remains FAIL because the authenticated task/agent Work/worktree-manager route, live owning-machine IPC, cross-machine backend relay, and DB-backed relay tests were not exercised together. This is an evidence gap, not an established product failure.

## Setup and scope

Read CLAUDE.md, RTK instructions, agent-browser and visual-qa guidance, and applicable coding guidance for the fixture repair. CodeGraph was unavailable on this worker's tool surface; used focused local file reads. No memory-derived project facts were used. This is an independent QA leaf; no subagents were spawned, per assignment. Full dual-oracle visual certification is therefore left to the parent review gate.

Verified port 5189 was free, started `pnpm exec vite --config scripts/qa/local-mr/vite.config.mjs`, and used an isolated Playwright browser with the actual `LocalReviewDialog` and fake API. Chrome executable verified at `/Users/shicheng_lei/.agent-browser/browsers/chrome-148.0.7778.97/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing`.

The existing fixture initially crashed because its fake response omitted `repositories`. Repaired only `scripts/qa/local-mr/main.tsx` to provide `[record.repository_path]`. The normal API schema is bypassed by that fake, so this is a fixture defect and does not establish a malformed-production-response defect. Added `scripts/qa/local-mr/review-check.mjs` as reproducible QA harness. Both files own fixture execution only; no new production abstraction, type escape, dependency, or logging path was introduced.

## Initial scenarios (15)

| ID | Priority | Scenario / observable result | Result / evidence |
|---|---|---|---|
| I01 | P0 | Opening unapproved review cannot merge | PASS, browser initial merge disabled |
| I02 | P1 | Tracked diff addition visible with line numbers and add/remove colors | PASS, browser + `01-open-desktop.png` |
| I03 | P1 | Source branch shown | PASS, browser source branch check |
| I04 | P1 | Target field initially main | PASS, browser target value check |
| I05 | P1 | Select untracked file, show its CJK contents | PASS, browser + `02-untracked.png` |
| I06 | P2 | Expand local commits | PASS, browser sees fixture commit |
| I07 | P1 | Enter Chinese review comment | PASS, browser field value; persistence not established by fake |
| I08 | P0 | Request changes updates state to 待修改 | PASS, browser + `03-changes-requested.png` |
| I09 | P0 | Requested changes keeps merge disabled | PASS, browser |
| I10 | P1 | Submit reopens review | PASS, browser state returns 待审查 |
| I11 | P0 | Approve enables merge | PASS, browser + `04-approved.png` |
| I12 | P0 | First merge click only opens confirmation | PASS, no merged marker before confirmation; `05-confirm.png` |
| I13 | P0 | Cancel merge preserves approval without merge | PASS, browser |
| I14 | P0 | Unapplied target draft blocks approve and merge | PASS, browser + `06-target-draft.png`; component unit asserts no request while typing |
| I15 | P1 | Restore target to main restores merge affordance | PASS, browser |

## Augmented scenarios after inspecting risk seams

| ID | Priority | Scenario / observable result | Result / evidence |
|---|---|---|---|
| A01 | P0 | Explicit confirmation enters merged state | PASS fake API browser; `07-merged.png`; real Git separately below |
| A02 | P0 | Merged state disables all four decision buttons | PASS, browser |
| A03 | P1 | Resize dialog to 390×844, CJK and controls fit | PASS after settled layout, `08-mobile.png` |
| A04 | P1 | Browser runtime exceptions absent | PASS, final results errors array empty |
| A05 | P0 | Local owner read/approve bypasses backend and local failure does not silently relay | PASS, `platform/local-review-routing.test.ts` four cases |
| A06 | P0 | Desktop runtime matching selects direct versus remote ownership | PASS, `src/shared/local-review-routing.test.ts` |
| A07 | P0 | Stale/dirty snapshots and conflicts preserve Git state | PASS, Git package tests |
| A08 | P0 | Merge updates target checkout while preserving source | PASS, real temporary Git repository test |
| A09 | P0 | Journal write failure prevents ref write; target rewind rejected | PASS, Git package tests |
| A10 | P0 | Owner identity, current approval, overlap guard, reused task bindings | PASS, daemon tests |
| A11 | P1 | Review history survives snapshot changes and repeated old approval preserves later decision | PASS, daemon tests |
| A12 | P1 | Task entry passes displayed run/path; no comment creation side effect | PASS, code-review-context component test; dialog mocked |
| A13 | P1 | Agent Work filters by agent; workspace inventory includes both runtimes | PASS, local-worktree-reviews component tests; inventory/dialog mocked |
| A14 | P1 | Worktree grouping/cleanup confirmation/error behavior | PASS, three worktree-manager tests; daemon mocked |
| A15 | P0 | Backend relay completion disposal, cancellation, pending bound, operation ID through actual DB handlers | UNTESTED: handler TestMain skipped for missing DB role |
| A16 | P0 | Separate runtime processes + HTTP + database | UNTESTED: LOCAL_REVIEW_TEST_DATABASE_URL absent |
| A17 | P0 | Authenticated production task, Work tab and manager launch full dialog | UNTESTED: isolated fixture does not mount these routes |
| A18 | P0 | No cloud MR persistence in deployed service across actual remote review | UNTESTED as a deployed flow; transient relay loop test passes, source/security review required |

## Commands and actual outcomes

All commands run from repository root except Go, run from `server/`.

```text
pnpm --filter @multica/views exec vitest run issues/components/local-review-dialog.test.tsx platform/local-review-routing.test.ts platform/local-review.test.ts
  3 files, 9 tests PASS
pnpm --filter @multica/core exec vitest run api/local-review.test.ts types/local-review-diff.test.ts
  2 files, 6 tests PASS
pnpm --filter @multica/desktop exec vitest run src/shared/local-review-routing.test.ts
  1 file, 1 test PASS
pnpm --filter @multica/desktop exec vitest run src/renderer/src/components/worktree-manager.test.tsx
  1 file, 3 tests PASS
pnpm --filter @multica/views exec vitest run issues/components/code-review-context-section.test.tsx issues/components/local-worktree-reviews.test.tsx
  2 files, 3 tests PASS
../scripts/go-test-with-agent-cli-guard.sh -- go test ./internal/daemon ./internal/handler -run 'Test(LocalReview|RemoteReviewCommand|RemoteMergeCanRetry|ReviewPathGuard)' -count=1 -v
  daemon PASS 8.682s; 14 top-level tests executed, 3 inventory subcases
  TestLocalReviewRuntimeProcessHelper SKIP: subprocess only
  TestLocalReviewAcrossHTTPDatabaseAndRuntimeProcesses SKIP: LOCAL_REVIEW_TEST_DATABASE_URL not set
  handler SKIP: role "multica" does not exist, SQLSTATE 28000
../scripts/go-test-with-agent-cli-guard.sh -- go test ./internal/daemon/localreview -count=1 -v
  9 tests PASS, 1.786s
node scripts/qa/local-mr/review-check.mjs
  19 checks PASS; errors: []
```

The guard reported no ambient agent CLI invocation. Handler process exit code zero is NOT evidence of executed handler tests. No `.env`, token, cookie, auth configuration, or database credentials were inspected.

## Browser artifacts and capture quality

Fresh final results: `.omo/evidence/mr-qa-browser/results.json`. Eight captures, all verified PNG signatures: seven 1440×1000 desktop frames and one 390×844 narrow frame. Direct visual inspection found readable CJK, actual selectable DOM diff rows, clear selected-file weight/background, explicit merge branch/commit pair, and reachable narrow-layout controls. No reference target was supplied, so no pixel-fidelity score is asserted.

Early harness attempts used two incorrect Chinese labels and an exact text locator that omitted the rendered line-number descendants; these were corrected against the DOM, not by changing product UI. A synchronous screenshot immediately after viewport resizing caught stale geometry (dialog x approximately -381); a fresh-page probe at 390px and a bounded settled-layout assertion both showed correct width approximately 370.5px. Final screenshots/results replace these invalid early artifacts; do not report them as product bugs.

Remaining visual coverage: loading/error/queued/running states, multi-repository selector, history overflow, empty diff, long paths/diffs, dark theme, intermediate animation frames and full production route context were not captured. Initial and final state safety is covered more broadly by focused tests, but this is not a substitute for those real-browser states.

## Blockers and handoff

No confirmed product defect in executed scenarios. Block full acceptance on A15–A18 and uncaptured production entry surfaces. Parent should keep these explicit rather than converting the isolated fixture PASS into an end-to-end production PASS. Screenshots are available for parent independent visual reviewers. Fixture server is the only service started by this worker and is stopped after evidence capture; browser processes are closed in finally blocks. Harness-only files remain for reproducibility.
