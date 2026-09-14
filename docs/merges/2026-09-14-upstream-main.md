# Upstream merge, 2026-09-14

## Inputs

- Fork main: `d383ebd9b860c5ef8f4905c057bae44543ae2670`.
- Upstream main: `a9e82c79739446111b8ca9acbb256f584072d20d`.
- Common ancestor: `9fab6da917953c6f1a967a17beef045aff3d0ce5`.
- Divergence: 60 fork-only commits and 61 upstream-only commits.

## Resolution

- Use upstream's extracted window toolbar, browsing history, long-press navigation, and sidebar clearance; retain the fork's localized refresh button, reload action, and native refresh binding.
- Keep both runtime-mirror/pin schema tests and upstream transcript-truncation tests.
- Keep runtime mirror sidebar fixtures alongside the upstream unread-summary behavior.
- Combine all concurrent-index cleanup hooks. Keep both mirror detachment and DSH install-wait tracking when runtimes are demoted.
- Use upgraded upstream dependencies while retaining the fork's screenshot and WebRTC dependencies. Regenerate Go module metadata.
- Give the inbox test-only truncation helper a specific name to avoid colliding with the fork's mirror-event helper.

## Migration identity and ordering

The independently shipped histories reused numeric migration prefixes. The numeric uniqueness check remains enabled. The following files use a reserved `900` prefix while `migrations.ExtractVersion` preserves their exact historical database-ledger identities:

- `900451_notification_bot` through the notification indexes at `900454_notification_bot_delivery_ready`.
- `900455_issue_goal_mode`.
- `900457_pinned_item_runtime_mirror`, `900458_runtime_mirror_event`, and `900459_runtime_mirror_event_index`.
- `900468_drop_reference_only_column`, resolving the upstream collision with `468_comment_deleted_at`.

For example, `900451_notification_bot.up.sql` still records/checks `451_notification_bot` in `schema_migrations`. Both directions use the same identity. Existing deployments therefore skip previously applied SQL without rewriting the ledger. New migrations must not reuse these filenames or identities. The alias list is explicit: other filenames beginning with 900 are not remapped.

Fresh installation and upgrade from the fork's previous main were executed on disposable PostgreSQL databases. A mirror event inserted before the upgrade survived, and the notification table and mirror index remained present. Re-running after renumbering skipped the existing ledger entries.

## Verification

- Typecheck: all nine non-mobile workspace targets passed.
- Core: 158 test files, 1,915 tests passed.
- Views: 450 test files, 5,214 tests passed.
- Desktop: 67 test files, 701 tests passed, including refresh alongside upstream history controls.
- Web and desktop production builds passed.
- SQLC regeneration produced no generated-code changes.
- The full regular Go suite and the separately throttled agent suite passed with race detection against disposable PostgreSQL. Test connections use TCP because older backfill fixtures rewrite database URLs and do not support socket-query URLs.
- Focused lint, Go vet, migration tests, deployment-source contracts, and desktop release-script contracts passed.
- An isolated built server returned HTTP 200 for health and advertised local MR, paged review, and selective merge support.

Native/browser interaction was not performed, honoring the user's earlier instruction. Web build logged the existing CSS highlight warnings and a rate-limited GitHub release lookup; the build completed successfully.
