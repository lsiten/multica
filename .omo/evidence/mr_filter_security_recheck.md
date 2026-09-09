# Configured-filter security delta recheck

Verdict: **PASS for the previously observed configured clean/process execution boundary.** No remaining blocker was found in this bounded delta. This is not full-product approval or a claim that arbitrary Git configuration is sandboxed.

Reviewed 2026-09-08 against current dirty files on the supplied HEAD `9da4ed8163a377e59b6a74d13d89c5599f277093`. Authoritative repository guidance in root `CLAUDE.md` and `RTK.md` was read. CodeGraph has an index but did not resolve the dirty `InspectionFilterOptions` symbol; exact scoped source reads provided the evidence. Scope: `localreview/filters.go`, the Git wrapper/read call sites in `localreview/snapshot.go`, `worktree_cleanup.go`, `localreview/read_safety_test.go`, and prior `mr_security_delta_review.md`.

## Why the prior finding is closed

- `filters.go:15-25` reads configuration **names only**, using a NUL-delimited query for clean/process keys. No-match is accepted; other command errors fail inspection before status/diff. The query does not perform a worktree conversion or expose command values.
- `filters.go:27-46` deduplicates filter prefixes and supplies three separate invocation-local Git options per driver: empty clean, empty process, and required=false. Clearing process as well as clean avoids the long-running-filter alternative; required=false lets raw inspection continue for normally required drivers. Arguments are passed directly to `exec.CommandContext`, without shell interpolation. Excessive unique drivers fail closed at 128.
- `snapshot.go:55-67` adds these options before status/diff arguments. Actual read status and tracked-name/per-file diff paths at `snapshot.go:141,149,159` enter this wrapper. Existing external-diff/textconv suppression remains on these diff calls.
- `worktree_cleanup.go:121-135` uses the same helper for its status/diff operations. Helper errors return before those commands. This shared wiring is source-verified; a dedicated daemon wrapper configured-filter marker test was not present in the selected test run.
- `read_safety_test.go:47-85` drives real `Read` for clean and process configurations separately. Each fixture has matching attributes, a harmless marker-producing executable, required=true, and changed working content. Both runs succeed, leave no execution marker, expose `+raw working content`, and verify the configured command and required=true remain unchanged. This checks the relevant settings, not a byte-for-byte comparison of the entire configuration file.

The threat still requires local Git configuration control or an already configured filter plus matching attributes. A repository's tracked attributes alone do not define a shell command. The closed finding is configured local command side effects during authorized inspection, not unauthenticated remote RCE. Concurrent replacement of the Git executable or mutation of configuration between enumeration and execution requires a distinct active local-OS adversary assumption and was not tested or claimed covered.

## Filtered-file semantic limits

Disabling these filters changes comparison semantics intentionally. The working side is not passed through configured clean/process conversion, while the committed side remains the stored Git blob. For LFS, a hydrated working file may therefore compare against an LFS pointer, producing a large/binary patch or an output-budget error; filtered repositories can appear dirty and be retained by cleanup even when ordinary Git considers them clean. Generic filters that normalize content can similarly produce extra differences. Git's normal stat/index shortcuts and other built-in conversions still apply: this change should not be advertised as an unconditional byte-for-byte filesystem scan or LFS-aware review.

These are compatibility/representation limitations rather than a reason to re-enable command-producing filters. The bounded tests establish changed raw text visibility, not hydrated-LFS equivalence, clean filtered-worktree behavior, every attribute/conversion combination, or concurrent filesystem/configuration mutation. No additional blocker is inferred from those untested scenarios.

## Executed verification

```sh
./scripts/go-test-with-agent-cli-guard.sh go -C server test -race ./internal/daemon/localreview ./internal/daemon -run 'Test(ReadDisablesFiltersAndShowsRawWorkingContent|ReadDoesNotExecuteConfiguredFSMonitor|ReadStopsAtAggregateBudgetBeforeLaterFiles|WorktreeGit)' -count=1 -timeout=90s -v
```

Localreview PASS (1.989s): clean and process marker/raw-content/config-preservation cases, fsmonitor case, aggregate-budget case. Daemon reported **no tests to run** for this selector; its package success is not counted as daemon regression coverage.

```sh
./scripts/go-test-with-agent-cli-guard.sh go -C server test -race ./internal/daemon -run 'TestWorktree' -count=1 -timeout=90s -v
```

Daemon PASS (1.870s): existing worktree cleanup/preservation/authorization/batch/replay tests. This supplements the source review but does not independently prove configured-filter suppression in `worktreeGit`.

Both commands exited zero, with no race report or ambient agent CLI invocation reported. Tests created and cleaned disposable fixtures; no user checkout content/configuration was modified, no real agent CLI ran, and no secrets, `.env`, or configuration dump was read. The only authored workspace artifact is this review. No authenticated UI, cross-user relay, full suite, or LFS-specific acceptance was performed.
