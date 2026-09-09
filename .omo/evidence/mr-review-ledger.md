# Local MR review ledger

Review target: uncommitted implementation on HEAD
`9da4ed8163a377e59b6a74d13d89c5599f277093`.

Tracked diff SHA256 at collection:
`6cb4ad972e48440c6af736eb69444d70ab0435da631d4fee69db93f8c6f371fd`.
Untracked source/check fixture manifest SHA256 (apps/packages/server/scripts/qa):
`5518b9c68da58ec9e72042ebd9fed9f066767807bc5decfa0cd619fe449e9cd2`.
These identify a dirty snapshot, not a released build or a clean commit verdict.

| Lane | Verdict | Artifact |
| --- | --- | --- |
| Goal | FAIL / conditional owner-Web scope plus acceptance gap | mr_goal_review.md |
| Code | FAIL | mr_code_review.md |
| Security | FAIL | mr_security_review.md |
| Context | FAIL | mr_context_review.md |
| QA | FAIL full acceptance / executed subset passed | mr_qa_review.md |

No lane PASS coverage is claimed. All completed findings are retained; code
changes after this snapshot require fresh applicable review, not a reuse of old
approval. Independent reviewers confirmed three primary remediation items:

1. Reject oversized live snapshots incrementally, before retaining all files.
2. Preserve ordinary managed-task MR receipts even without directory bindings.
3. Prevent Git review inspection from executing configured fsmonitor commands.

Owner-machine ordinary Web direct access is a scope ambiguity; the current direct
transport is Electron IPC. Arbitrary multi-server support was not explicitly
requested and is not a substitute for single-server/desktop acceptance.

## Remediation delta

HEAD remains `9da4ed8163a377e59b6a74d13d89c5599f277093`.
Tracked diff SHA256: `03bcfe24a6b2d964b589fa874ec163f3a37bbe57027d8998397e83d72297dce7`.
Untracked source manifest SHA256: `5063fa7f7dcac188467b20d1c13bab12eb6fad76fc3363a0ac4b6ce0975ff19c`.

- Code delta lane: PASS for aggregate retention and ordinary receipt archival,
  artifact `mr_code_delta_review.md`. This is not a full code-lane/product PASS.
- Security delta lane: FAIL, artifact `mr_security_delta_review.md`. fsmonitor
  and retention fixes pass, but configured Git clean filters still execute on
  live diff inspection. Requires local Git config and matching attributes;
  do not describe as unauthenticated remote code execution.
- QA completed: 19 isolated browser checks and eight screenshots are available
  under `mr-qa-browser/`; complete authenticated acceptance remains unproven.

Overall review remains FAILED. No old PASS or test subset is treated as release
approval. Next security action: disable configured clean/process filters for MR
inspection without silently producing an incorrect diff, then test and re-audit.

Implementation follow-up: configured clean/process commands now receive empty
per-invocation overrides for status/diff, with required=false. Real marker tests
pass for both variants and assert raw diff visibility and unchanged repository
configuration. Shared worktree inspection uses the same helper. Independent
security re-review is still pending; overall review is not approved.

## Filter recheck and database authorization evidence

- Filter-security delta: PASS, `mr_filter_security_recheck.md`, HEAD
  `9da4ed8163a377e59b6a74d13d89c5599f277093`, dirty source scope only.
- Source SHA256: filters.go `5f0af077d8831971e15245a4ece29264b4a05ca1aa8f5146a2fa342ec24e058c`;
  snapshot.go `169bc0838788b6422489f54ce0191bebd82f09ffd2a50ba6a03839c81510cb87`;
  read_safety_test.go `3b183cff57d54e68408b20db0d1ef2abe38eebccbcf86a82faf948071f2db4e2`;
  worktree_cleanup.go `76975fbc0058a60f54b3c2d5984a3fa1002003f2db77ce56716be36a9f16960a`.
- Handler suite verified with explicit fresh DATABASE_URL and verbose execution:
  7 forwarding authorization cases, 4 owning-credential claim cases, 3 relay
  lifecycle/bounds tests, 4 required-operation-ID cases passed with `-race`.
  These use trusted actor stamps at the handler boundary, not real login tokens.
- Evidence correction: handler TestMain exits 0 when DB unavailable. Earlier
  no-DB handler exit codes are NOT execution evidence. This fresh DB run replaces
  those claims for the named tests, not the entire handler suite.
- Full authenticated UI/IPC acceptance and other goal gaps remain; scoped PASS
  records above do not approve the complete feature.

## Production JWT middleware integration

The current process test replaced the identity-stamping fixture with production
Auth middleware plus workspace/human gates. On a fresh DB, valid test-signed JWTs
completed real read/approve/merge in separate worker processes. The same run
rejected unauthenticated identity spoofing, wrong signatures, and cookie writes
without CSRF; the final check confirms cloud MR tables are absent. Ran with
`-race -count=1 -v`, PASS. Key and users were test-only, and the test DB was removed.
This supersedes the authentication-stamp limitation for that process scenario,
but does not claim app sign-in UI/deployed-user-session acceptance.

## Legacy/profile final delta

HEAD `9da4ed8163a377e59b6a74d13d89c5599f277093`, dirty source.
- Security: scoped PASS, `mr_legacy_final_security.md` (source audit).
- Code: initial FAIL, `mr_legacy_final_code.md`, for losing runtime identity on
  target/repository changes. Fixed with a task/workspace-scoped observed Query
  cache, independent of snapshot path/target.
- Fresh cache recheck: scoped PASS, `mr_runtime_cache_recheck.md`; component SHA256
  `f2f78471a4d2b42d192a672d0a212b07aa87f3b5ce8f63fe9b860c8d98c93e15`.
  Reviewer ran six tests; root then added and passed the repository-switch case,
  bringing the dialog suite to seven tests with the same production source.
No source audit is represented as a production-account navigation recording.
