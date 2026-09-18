# Upstream merge, 2026-09-17

## Inputs and retained behavior

- Fork main: `90536f781c02c634da4d66b1811cf5bde40c95c0`.
- Upstream main: `7e4758ac1a94e9ff843696333364610bb8d4bbf7` (70 new upstream commits).
- Merge preserves fork history and features, including Goal Mode, local MR and worktree management, notification bots, runtime mirroring, and the macOS daemon launch/signing changes.
- Upstream additions include session renewal, archived inbox pagination, issue status categories, UI Lab, French localization, and runtime/maintenance changes.

## Conflict resolutions

- Export both upstream `BuiltInIssueStatus` and fork Goal Mode types.
- Retain sidebar runtime-mirror fixtures and upstream invitation fixtures.
- Keep upstream's concurrent, cached migration corpus. Key it by the stable migration ledger identity so renamed fork migrations still match their existing cleanup hooks.
- Update the local MR process-test harness to the new authentication middleware signature.
- Add French translations for fork-specific features without replacing upstream translations. Translation keys and interpolation placeholders pass parity checks.
- Isolate fake Codex app-server tests with a temporary sessions directory. Previously terminal-usage fallback could traverse real user rollouts and block results beyond the test deadline. Keep explicit fixture directories and all existing deadline assertions unchanged.

## Evidence and limits

- All ten non-mobile typecheck targets passed; mobile typecheck passed separately.
- Core: 168 test files / 2,023 tests passed. Desktop: 70 files / 716 tests passed. Mobile: 27 files / 183 tests and iOS wrapper assertions passed.
- Focused view, MR entry, sidebar, dialog and locale checks: 264 tests passed. Issue-detail suite had 79 passes and one 5-second timeout; the remaining scenario passed separately. Earlier broad-view timeouts also passed separately without changing timeout limits.
- SQLC regeneration produced no generated-code delta. Deployment and desktop-release script contracts passed.
- Fresh migrations and upgrade from the prior fork main passed on disposable databases. A pre-upgrade runtime-mirror event remained present; notification and goal tables and historical migration identifiers were retained.
- Race-enabled migration/MR tests, including JWT/HTTP/separate-daemon-process scenarios, passed. A 150ms cancellation-watcher assertion failed under concurrent load and passed alone without source/test threshold changes.
- The regular backend run passed all packages except daemon, handler, and Lark during contention. The daemon's sole failing cancellation-watcher case passed separately; complete handler and Lark packages passed in a serial race-enabled rerun. No failing assertions or timeouts were weakened.
- After fixture isolation, the Codex first-turn timeout, first-item lifecycle, scanner-overflow cleanup, interrupt-deadline, Qwen and Grok focused race tests passed in 15.375s. The two original timeout-diagnostic regressions passed in 2.906s.
- Built backend started against the upgraded isolated database; `/health` and `/api/config` returned HTTP 200 with MR paging and selective merge enabled.
- Full-view runs were interrupted after repeated host-load timeouts. Web production-build attempts encountered external TLS failures and were stopped during prolonged resource contention; no successful full Web build is claimed for this merge.
- No graphical desktop/browser interaction or deployment was performed.
