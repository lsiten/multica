# Upload cleanup

The server runs the collector hourly by default, with seven days of retention.
It reclaims unbound uploads and attachments belonging to cancelled issues
(including custom `closed` statuses) or archived chats. Completed issues and
inbox notification archival do not qualify. Live runs protect their owners.
The latest owner update and the upload's age must both exceed retention.

Other retained records protect shared files, including avatars, text links,
attachment IDs, draft restores, document versions and source snapshots.
Local storage also scans orphan files, sidecars and interrupted writes. S3
reclaims tracked attachments; it does not scan the bucket for orphan objects.
Backups are not collected.

Cleanup permanently removes attachment records and bytes. Task/chat text stays,
but restoring an owner cannot restore purged files. Unsent uploads without a
retained reference also expire after seven days.

## Preview and apply

In a self-hosted container, preview uses the same database and storage settings
as the backend:

```sh
docker compose -f docker-compose.selfhost.yml exec backend ./cleanup-uploads
docker compose -f docker-compose.selfhost.yml exec backend ./cleanup-uploads --workspace <workspace-uuid>
docker compose -f docker-compose.selfhost.yml exec backend ./cleanup-uploads --apply --limit 100
```

The JSON report includes candidate keys, reasons, bytes, deleted objects and
failures. Preview is the command's default; `--apply` enables deletion.
Repeat apply to drain more than one batch. `--retention 168h` overrides retention
for that invocation (minimum `24h`). `--limit` accepts `1..1000` and bounds
attempts, including failures. Local bytes include sidecars and staging files;
S3 bytes use attachment sizes.

From a managed development checkout:

```sh
make env-exec ARGS="-- sh -c 'cd server && go run ./cmd/cleanup-uploads'"
```

The command itself does not load `.env`; pass the target environment explicitly.
Do not point multiple databases at a shared upload directory: the database must
describe every reference to that directory.

## Server configuration

| Variable | Default |
| --- | --- |
| `UPLOAD_CLEANUP_ENABLED` | `true` |
| `UPLOAD_CLEANUP_RETENTION` | `168h` |
| `UPLOAD_CLEANUP_INTERVAL` | `1h` |
| `UPLOAD_CLEANUP_DRY_RUN` | `false` |

Set `UPLOAD_CLEANUP_DRY_RUN=true` to log candidate counts and bytes without
deleting, or `UPLOAD_CLEANUP_ENABLED=false` to stop background collection.
The first round runs after one interval. Each round attempts at most 100
objects. Intervals below one minute are clamped; retention below 24 hours uses
the seven-day default.

Disk enumeration happens before locking. The primary database then briefly
locks reference tables to serialize restores and attachment binding against
the reference check and physical deletion. Busy rounds skip after a 100 ms
lock timeout; database work has a two-second statement timeout and the whole
locked round has a five-second budget. File identity, size and modification
time are checked again before removal; symlinks cannot escape the upload root.

Failed deletes keep attachment rows for retry while other objects in the batch
can proceed. A crash after physical deletion but before database commit leaves
metadata that a later round settles through an idempotent delete. Context
cancellation stops the round. Operators should inspect repeated failure logs
and fix storage permissions or availability.
