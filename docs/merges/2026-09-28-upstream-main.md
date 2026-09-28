# Upstream integration — 2026-09-28

## Inputs and history

| Source | Commit |
| --- | --- |
| Local `main` before integration | `4863eed01e55fd246b261ccc452f8015e50ee006` |
| `origin/main` (`lsiten/multica`) | `3bfed9c636733a4513075e6efeae5e9197f632ae` |
| `upstream/main` (`multica-ai/multica`) | `ea94c7cd5bbce9c8e1f28c5fa049c47ee7651d02` |

The local working tree was clean. The local backup branch is
`backup/pre-upstream-sync-20260928-4863eed`.

Origin had replayed the local feature series onto a newer upstream baseline.
The local/origin merge base is `8ce795b00718bd5d527153a6ded1de36e943be12`;
the origin/upstream merge base is `be085b1222a8adac1eff516d449eb8d718931f3f`.
The range comparison found 138 identical patches and 28 adjusted counterparts.
Replaying all commits from the old local history would duplicate these changes.

Integration starts from origin, merges upstream, restores the local-only
integration work below, and then records the old local head as a merge parent.
The final history must contain all three input commits. The ancestry merge
records reconciled history; the content audit below is what establishes that
the local changes were preserved.

## Conflict decisions

The upstream merge produced 34 conflicted paths. Resolutions retain the fork's
runtime mirror, virtual screen, local review, notification, and voice behavior
alongside upstream task supplements, joined wakeups, attribution, settings
navigation, and search-index changes.

- Keep both desktop CLI build helpers: native CGO selection and normalized
  package version derivation. Include the new module in the packaging fixture.
- Keep desktop refresh and history controls with the upstream toolbar layout;
  reserve space for the five controls and test the combined behavior.
- Move the local worktree review entry into the upstream Code settings tab;
  retain upstream label exports and settings permission gates.
- Preserve both mirror and wakeup inbox types, labels, and translated copy.
- Keep mirror-source validation in chat/claim paths and the upstream ordering
  of attribution hydration after authorization gates.
- Keep Codex approval context and completion observation while integrating
  upstream supplement handling.
- Regenerate sqlc models and queries from the combined SQL sources.

## Local integration work restored

- Nine fork migrations keep their `900451` through `900468` filenames and
  exact `ExtractVersion` aliases for previously recorded ledger identities.
  Restore the identity regression and adapt migration test corpus keys and
  notification-index fixture paths to the renamed files.
- Restore 145 French translations across agents, issues, runtimes, and
  settings, preserving newly added keys.
- Restore the desktop refresh/history regression.
- Restore `ACManagedWindows` stubs in the two native regression probes and
  remove the duplicate native declaration.
- Preserve the September 14 and September 17 integration records.

The audit examined the local branch's 43 merge commits in addition to its
replayed patches. Do not reintroduce old comment steering: upstream commit
`8c9f865e3e` intentionally reverted it.

## Verification

Verification uses an isolated checkout and a dedicated test database. The
test runner's existing fake-agent guard remains enabled; real-agent smoke
tests, deployment, publishing, and remote pushes are outside this integration.

| Check | Result |
| --- | --- |
| `pnpm install --frozen-lockfile` | Passed |
| `pnpm typecheck` | 10 tasks passed |
| `pnpm lint` | 7 tasks passed; 30 existing warnings |
| Core, web, docs, UI Lab tests from `pnpm test` | 2,745 tests passed |
| Views `vitest run --maxWorkers=2` | 512 files, 6,112 tests passed |
| Desktop `vitest run --maxWorkers=2` | 90 files, 935 tests passed; 2 tests skipped |
| Mobile typecheck, lint, test | Passed; 234 tests plus iOS wrapper checks |
| Desktop packaging helper tests | 35 tests passed, included in desktop total |
| Go regular packages with `-race -count=1` | Passed with the two package reruns described below |
| Go agent packages with `-race -count=1 -p 2 -parallel 2` | Passed |
| Migration package with `-race -shuffle=on -count=1` | Passed against the dedicated database |
| Fresh migration through the environment helper | Passed |
| Native session/fence regression probes | Both built and ran successfully |
| Repeated `make sqlc` | No generated-file changes |
| `git diff --cached --check` | Passed |

The first frontend run used unrestricted concurrency and encountered timeouts.
Complete views and desktop reruns passed with two workers and unchanged test
timeouts. The desktop clearance tests and a removed execution-log fold step
were adapted to the combined behavior without dropping functional assertions.

CLI tests initially discovered the real task marker in this checkout's parent
directory. The race-instrumented test binary passed from an isolated temporary
working directory, with inherited platform credentials removed and the fake
agent guard retained. No task marker or production guard was changed.
One daemon test inherited the host's granted accessibility permission; its
fixture now explicitly supplies an unavailable injector, restores it afterward,
and retains the original permission/no-display assertions. Its focused tests
and the entire daemon race suite passed.

Independent review covered all 34 conflicted paths and the restored local-only
changes. No unresolved review finding remains. Browser E2E, a real Electron or
mobile interface, native permission prompts, and release builds were not run.
The accompanying synchronization report records final commit IDs and the
verification-environment caveat from the initial compile-only command.
