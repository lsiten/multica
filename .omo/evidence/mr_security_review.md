# Local MR security review

Verdict: **FAIL — two medium-severity blockers remain.**

Scope: HEAD `9da4ed8163a377e59b6a74d13d89c5599f277093` plus the current uncommitted/untracked local-MR implementation, inspected 2026-09-08. This is a fresh review of direct mutations, not a renewal of an earlier audit. Source was not edited. CodeGraph was unavailable on this agent's tool surface, so scoped source inspection was used.

## Findings

### SEC-1 — Medium: review inspection can execute configured external commands

`server/internal/daemon/localreview/snapshot.go:55-59` disables hooks but inherits Git configuration; `:133` invokes `git status`. Git's `core.fsmonitor` command is independent of `core.hooksPath`, so a read-only review invokes it as the daemon user. Diff flags at `:141` and `:150` disable diff helpers/textconv, but do not address this path.

Confirmed with a disposable Git repository, not a user repository. Initialized `/tmp/multica-security-review.S9FGTK`, made one empty fixture commit, and set its local `core.fsmonitor` to `touch fsmonitor-executed`. Running the exact inspection wrapper options/environment:

```sh
GIT_TERMINAL_PROMPT=0 GIT_OPTIONAL_LOCKS=0 GIT_LITERAL_PATHSPECS=1 git --no-pager -c core.hooksPath=/dev/null -C /tmp/multica-security-review.S9FGTK status --porcelain=v1 -z --untracked-files=all
```

created `fsmonitor-executed`; terminal evidence was `EXTERNAL_FSMONITOR_EXECUTED=true`. Git supplies arguments to the hook, so the harmless fixture also contains files named `2` and a timestamp. The fixture is retained for inspection.

Impact and prerequisite: a configured command, including one installed by earlier task execution, runs when an authorized remote reviewer only requests a snapshot. This is a command-side-effect boundary failure, **not proof of an unauthenticated remote-code-execution bypass**; the reproducer requires control of local Git configuration. Severity is medium for that reason. Disable fsmonitor for inspection and audit the other command-producing Git extension points used by merge/status/checkout. Add a sentinel regression through the actual review read path.

### SEC-2 — Medium: live snapshot aggregate limit is checked after allocation

`server/internal/daemon/localreview/snapshot.go:146-155` appends all tracked patches and `:161-202` reads/appends all untracked files. The aggregate 8 MiB test occurs only at `:208-213`, after the entire collection is resident. Each individual file/command may be below 8 MiB while total retained data is arbitrarily larger. For example, 1,000 untracked 7 MiB text files require roughly 7 GiB of retained patch content before rejection (plus conversion/allocation overhead).

`server/internal/daemon/worktree_review.go:193-194` holds the process-wide review mutex during this read. An authorized review of a large or adversarial task checkout can therefore exhaust daemon memory and block other review operations. The 12 MiB relay response cap is too late to protect snapshot construction. This is source-proven; an OOM experiment was deliberately not run.

Enforce a cumulative budget before every append/read, pass the remaining budget into file reads, and stop at a bounded file count. The committed reader already checks incrementally (`localreview/committed.go:67-80`), although per-command buffers still use their own cap. Add a small aggregate-overflow regression that demonstrates stopping before processing later files.

## Controls verified by current source

- Direct handler requires bearer, matching profile, absent Origin, POST, and bounded request body (`worktree_review.go:125-145`). Direct mutations require a registered workspace runtime and exact persisted workspace/task/runtime binding (`:147-162`, `:201-208`). Renderer-supplied actor is replaced with `local-runtime:<runtime_id>`; desktop Zod request parsing also omits actor input (`packages/core/types/local-review.ts:5-16`).
- Remote server rejects machine credentials, loads the task inside the selected workspace, verifies runtime access and task issue access, restricts issue-less reviews to runtime owners, and restricts merge to the runtime owner (`handler/local_review_forward.go:19-79`). The router applies human-only middleware (`server/cmd/server/router.go:2305-2308`) inside workspace membership middleware (`:1861-1863`). `requireRuntimeReadAccess` enforces membership and public/private runtime access (`handler/runtime.go:661-681`).
- Relay storage is a process-local map capped at 64 pending exchanges; results are bound to workspace, runtime, random command ID and claim token; exchanges are deleted when the HTTP wait exits (`handler/local_review_relay.go:11-93`). No MR database writes or GitHub MR creation were found in the reviewed execution path.
- Source authorization canonicalizes requested paths, checks task/workspace ownership or a persisted external-directory binding, and refuses escape paths (`worktree_review.go:39-116`). Untracked reads use `os.OpenRoot`, regular-file checks and inode identity checks (`localreview/snapshot.go:165-189`). These checks do not establish a complete defense against an attacker with concurrent arbitrary filesystem write access; no such stronger claim is made.
- Review/merge compares the requested snapshot and requires approval. Merge rereads the exact snapshot; conflicts are computed before ref changes, and checked-out target updates use a local file receive-pack with an exact old-ref lease (`localreview/merge.go:19-100`). Git operations occur on the runtime machine. This file push is local transport, not a GitHub operation.
- Mutations acquire environment/source/repository locks; merge additionally checks active source/target task paths (`worktree_review.go:210-245`, `:305-320`). Recovery acquires the corresponding environment/source/repository locks (`local_review_recovery.go:17-58`).
- Idempotency checks action, snapshot, comment and actor for an existing operation ID (`worktree_review.go:263-274`); recovery checks the original request identity for redelivery and preserves the original actor/comment in recovery events (`local_review_recovery.go:68-73`, `:90-98`). Private prepared snapshots/requests are excluded from RecordView (`localreview/record.go:38-49`).

## Executed verification and limits

Executed through the repository's agent CLI guard:

```sh
./scripts/go-test-with-agent-cli-guard.sh go -C server test ./internal/daemon/localreview ./internal/daemon ./internal/handler -run 'Test(LocalReview|RemoteReview|WorktreeReview|Merge|CheckedOutTarget|Read|CommittedReview)' -count=1
```

Results: localreview PASS (2.256s), daemon PASS (8.845s), handler PASS (0.758s). No ambient agent CLI invocation was reported. The current local-owner test verifies approval attribution, actual fixture target ref update, recovery after a simulated lost receipt, and no second merge on retry.

The test success does not cover SEC-1 or SEC-2. No live authenticated cross-user database-backed server acceptance, Electron UI acceptance, adversarial symlink race, or load/OOM test was executed in this audit. Only the disposable fixture Git repository was written; no user repository Git operations, external publication, configuration dumps or real agent CLI execution occurred.
