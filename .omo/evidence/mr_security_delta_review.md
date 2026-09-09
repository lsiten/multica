# MR security delta review

Verdict: **FAIL — the two reported fixes are effective, but a confirmed configured clean-filter execution path remains in live snapshot inspection.** Scope is this security delta, not full product signoff.

Reviewed 2026-09-08 against the current dirty source on the supplied HEAD `9da4ed8163a377e59b6a74d13d89c5599f277093`. No production source, user checkout Git state, credentials, or configuration dumps were modified/read. Tests and the explicit reproducer wrote disposable fixture repositories only. CodeGraph was queried but did not locate the dirty localreview symbols; scoped source reads supplied the evidence.

## Fixed findings

- Prior SEC-1 fsmonitor: `localreview/snapshot.go:58` now passes per-command `core.fsmonitor=false`, retaining disabled hooks and diff/textconv controls. All package Git invocations route through `git`/`trimmed`, including live/committed reads, target checks, merge, and recovery helpers. `worktree_cleanup.go:123` applies the same override to `worktreeGit`, covering source root resolution in `worktree_review.go:61` and cleanup status/list/ref operations. The receiver is launched by the configured Git command; this review did not separately instrument receiver subprocess configuration inheritance.
- Prior SEC-2 retained patch growth: `snapshot.go:155-159` and `:208-212` check remaining capacity before tracked/untracked append. Retained patch strings cannot accumulate past 8 MiB. A currently processed file still has its own bounded 8 MiB allocation, so this is not a strict 8 MiB total process-memory limit. Name/metadata buffers and file count also remain outside the patch-byte accounting. The earlier arbitrary accumulation of multi-megabyte patch strings is closed.
- `read_safety_test.go` exercises the actual `Read` path with a shell fsmonitor marker and checks aggregate rejection before a later symlink sentinel. Both passed. Its aggregate regression directly covers untracked text files; the tracked branch was source-verified, not independently overflow-fixtured here.

## Remaining SEC-3 — Medium: configured clean filters run during live diff

At `server/internal/daemon/localreview/snapshot.go:151`, `git diff --no-ext-diff --no-textconv --no-renames --binary <base> -- <name>` reads a tracked working file through Git's configured clean conversion. Disabling external diff, textconv, hooks, and fsmonitor does not disable `filter.<name>.clean`. A matching `.gitattributes` entry plus local Git filter configuration permits a command to run as the daemon user during a snapshot request.

Confirmed with disposable fixture `/tmp/multica-security-delta.DdJ84n`:

1. Initialize a Git repository, commit `.gitattributes` containing `app.txt filter=reviewmarker` and `app.txt` containing `base`.
2. Set fixture-local `filter.reviewmarker.clean` to `touch /tmp/multica-security-delta.DdJ84n/clean-filter-called; cat`.
3. Change fixture `app.txt` to `changed`.
4. Run the production diff wrapper shape:

```sh
GIT_TERMINAL_PROMPT=0 GIT_OPTIONAL_LOCKS=0 GIT_LITERAL_PATHSPECS=1 git --no-pager -c core.hooksPath=/dev/null -c core.fsmonitor=false -C /tmp/multica-security-delta.DdJ84n diff --no-ext-diff --no-textconv --no-renames --binary HEAD -- app.txt
```

Observed expected diff plus `CLEAN_FILTER_EXECUTED=true` from testing the marker's existence. An earlier status call with the same wrapper did not create the marker for this size-changing fixture; the diff call did. The fixture is retained for inspection.

Prerequisites: control of local Git configuration (or an already configured filter) and a matching attributed tracked file. A tracked `.gitattributes` alone cannot define an arbitrary shell filter command. This is an authorized read causing configured local command side effects, **not an unauthenticated remote RCE claim**. It is the same read-only boundary concern as the original fsmonitor finding.

Remediation: define and enforce how snapshot inspection handles configured clean/process filters, including the effect on Git LFS or other intentionally filtered content. Either isolate inspection from command-producing filter configuration or reject affected live snapshots before a filter-triggering operation. Add a real marker regression through `Read`, including the process-filter variant if that configuration is supported. Do not describe fsmonitor/hooks/diff flags as a complete no-command-execution boundary.

## Verification

Executed:

```sh
./scripts/go-test-with-agent-cli-guard.sh go -C server test -race ./internal/daemon/localreview ./internal/daemon -run 'Test(Read|CommittedReview|Merge|CheckedOutTarget|LocalReview|Worktree)' -count=1 -timeout=90s
```

Results: localreview PASS (3.661s), daemon PASS (9.650s), no race report, and no ambient agent CLI invocation reported. The filter reproducer ran against real Git in a disposable repository, with harmless marker creation only. It was not a new Go test through `Read`; the call-site mapping above is source-backed.

The delta changes do not add cloud MR persistence or change local-owner direct-daemon/remote-relay routing. The relay remains an in-memory pending-exchange map with deletion on completion; full authorization/relay acceptance was not repeated here. No Electron, authenticated cross-user server, OOM/load, or concurrent filesystem-attacker acceptance was performed. No real agent CLI ran.
