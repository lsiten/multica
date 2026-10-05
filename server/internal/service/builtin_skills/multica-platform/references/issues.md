# Issues

Product contracts the runtime brief does not fully encode.

- [PR linking](#pr-linking)
- [Reading a linked PR's real state](#reading-a-linked-prs-real-state)
- [Custom properties: typed workflow state](#custom-properties-typed-workflow-state)
- [Status changes have server side effects](#status-changes-have-server-side-effects)
- [Who else is running right now](#who-else-is-running-right-now)
- [Rerun, cancel, and inspect a run](#rerun-cancel-and-inspect-a-run)
- [Subscribers](#subscribers)
- [Labels](#labels)
- [Sub-issues: todo starts work now, backlog parks it](#sub-issues-todo-starts-work-now-backlog-parks-it)
- [Charts and files in a comment](#charts-and-files-in-a-comment)
- [Incorrect to correct](#incorrect-to-correct)
- [Run handoffs and member replies](#run-handoffs-and-member-replies)
- [Issue wakeups](issue-wakeups.md)

To attach a local file to an existing issue description, use `multica issue update <id> --attachment <local-path>`. The CLI appends the file's Markdown reference to the end of the description; to replace an image, also use `--description-file` to remove the old reference. Do not put local filesystem paths in the description. `--description-file` and `--attachment` read a path **inside the current working directory** unless `--allow-external-file` is set (MUL-4252), which stops a stale file from another run or environment from being picked up. `--attachment` is repeatable (repeat the flag for multiple files); on `issue create` you can instead bind already-uploaded attachments with `--attachment-id <uuid>` — `issue update` has no such flag. To feed a description from a pipe use `--description-stdin` (a heredoc `--description` can swallow trailing flags, #4182).

## PR linking

A PR is linked to an issue when its **title** or **branch name** contains a
routable issue key (`PREFIX-NUMBER`, e.g. `MUL-123`), or when its title or body
puts the key **right after a closing keyword** (`Closes` / `Fixes` /
`Resolves`, optional `:` then whitespace). A key that appears in the body as a
bare mention links nothing. People can also link a PR by URL or remove one on
the issue page; a removed PR is not linked again by later webhooks.

```text
MUL-123: add the thing the issue asks for     # key in title  → links
agent/dana/mul-123-add-the-thing              # key in branch → links
Closes MUL-123   (body)                       # key after a keyword → links
Related to MUL-123   (body only)              # no link
```

While a PR is open, its automatic links follow the live title, branch, and
body: removing the key drops the link. After merge or close, existing links stay.

### Default for code-changing issue work

When an issue run changes code in a checked-out GitHub repo, the default handoff
is to open or update a PR before posting the final Multica issue comment, unless
the user explicitly asked for a local-only change or no PR. This is a default, not
an unconditional command: if no code changed, say no PR is needed; if PR creation
is blocked by auth, failing tests, or missing remote state, report that blocker
instead of pretending the run is complete.

To make the PR show on the issue, put a routable issue key in the PR **title**
(preferred) or the **branch**. A key that appears only as a bare mention in the
body links nothing.

```text
MUL-123: fix login redirect        # key in title → links
Part of MUL-123                    # body mention only → no link at all
```

In the final issue comment, include the PR URL when a PR exists. If the task did
not produce a PR because no code changed or the user asked not to create one, say
that explicitly.

## Reading a linked PR's real state

When a step depends on PR state, query Multica's link table — do not infer it
from branch names, GitHub search, memory, or stale values left on the issue by
an earlier run.

```bash
multica issue pull-requests <issue-id> --output json
```

Returns `{"pull_requests": [...], ...}`. Each element of `pull_requests` exposes:

- `number`, `html_url`, `title`
- `link_source` — why the PR is on the issue: `title`, `branch`, `manual`, or
  `auto` (any other automatic link, such as a closing keyword in the body).
- `state` — the PR lifecycle as a **single enum**, one of `merged`, `closed`,
  `draft`, `open`. There is no separate `draft` or `merged` boolean in the
  response; the server folds them into `state` (merged wins, then closed, then
  draft, else open).
- `merged_at` — non-null once merged; a second confirmation of `state: merged`.
- `provider` — `github`, `forgejo`, `gitea`, or `gitlab`.
- `mergeable_state` — mirrors GitHub (`clean` / `dirty` surfaced; other values
  round-trip as unknown; retained for compatibility).
- GitHub API snapshot fields: `snapshot_available`, `mergeable`,
  `merge_state_status`, `checks_rollup`, `checks_total`, `checks_passed`,
  `checks_failed`, `checks_running`, `failed_check_names`,
  `snapshot_fetched_at`, and `snapshot_stale`. `snapshot_available == true`
  means the feature is enabled and the snapshot matches the PR's current head.
  Only then does `checks_rollup == null` mean "no checks"; false means the
  snapshot feature is disabled, has not fetched yet, or only has an old head.
- `checks_conclusion` — coarse CI compatibility status: `passed`, `failed`,
  `pending`, or `null`. GitHub derives it from the current API snapshot;
  Forgejo/Gitea/GitLab derive it from webhook commit statuses. Backed by the
  provider-appropriate check counts.

So "is it merged?" is `state == "merged"` (or `merged_at != null`); "is it still
a draft?" is `state == "draft"`; coarse CI status is `checks_conclusion`.

If the command returns no linked PRs after a PR was opened, check the syntax
first: the key must be in the PR title or branch, or right after a closing
keyword in the body — a bare body mention does not count. When the syntax is the
problem, editing the title re-runs the scan. If a person removed
the PR from the issue, it stays removed until someone links it again.

If the key is already written correctly and the list is still empty, stop editing
the PR blind: another no-op edit cannot fix an integration that never received the
event. Check the integration side instead — whether the app is installed on that
repository, whether the installation is bound to this workspace, whether
auto-linking is turned off for the workspace, and whether the event reached the
platform at all. A delivery that failed is not retried on its own, but it can be
redelivered once the receiving side is fixed. Report what you found in the result
comment rather than repeating the edit.

## Listing and ordering issues

`issue list` reads one page at a time, with a server maximum of 100 issues.
Advance `--offset` by the number of issues actually returned. If the server
cannot count matching issues, it returns `failed to count issues` as an error;
do not treat that failure as an empty or complete list. Older servers can
substitute the page length for a failed count, so that value alone is not proof
that all matching issues have been read.

`issue reorder` reads the issue's project-scoped status column before writing
its new position. When a legacy total is unavailable or no larger than its
page, it reads through an empty page. A failed request, malformed page, or
duplicate issue stops the operation before any position write. This protects
against truncated or repeated pages, but does not promise a snapshot across
concurrent edits. There is no CLI bulk-export or `--all` mode.

## Custom properties: typed workflow state

Workspaces may define custom issue properties (Severity, Environment, QA
Status, Reviewer, ...). They are the place for durable, typed issue state:
values are validated against the definition (select options, date format,
http(s) URL, member reference), visible in the issue sidebar, and addressed
by name.

- Read what exists before writing: `multica property list` shows the catalog;
  `multica issue property list <issue-id>` shows values set on the issue.
- Set values by property name and option name — the CLI translates to ids:

```bash
multica issue property set <issue-id> --name Environment --value staging
multica issue property set <issue-id> --name Platforms --value "iOS,Android"
multica issue property set <issue-id> --name Reviewer --value Bohan
multica issue property unset <issue-id> --name Environment
```

- A validation error lists the legal options — fix the value and retry.
- `actor` / `multi_actor` properties (Reviewer, Escalation contact, ...) hold
  workspace members only. `--value` takes a member name, email, UUID, short id,
  or an explicit `member:<uuid>`; `multi_actor` takes a comma-separated list
  (duplicates dropped, order kept, max 20).
- Definitions may include an optional catalog icon for visual identification;
  it does not change the property's type or value validation.
- Agents cannot create or edit property definitions (owner/admin humans only).
  If a needed property does not exist, propose it in a comment instead.
- Where state belongs: workflow state a human should see and filter by goes in
  a property; the stage the issue is at goes in its status; everything else —
  what you did this run, what you found — goes in the result comment.
- `issue list` filters and sorts by property with the same name addressing:

```bash
multica issue list --property "Impact=High" --property "Impact=Medium" --output json
multica issue list --property "QA Status=__none__" --status in_review --output json
multica issue list --sort property:Impact --direction desc --output json
```

- `--property` takes one `Name=Value` per flag. Repeating the same property
  matches ANY of its values; different properties must ALL match. Values are
  option names or ids (select types), `true`/`false` (checkbox), a member
  name/email/id (actor types), or the value itself for text, url, number,
  and date (`YYYY-MM-DD`). The reserved value `__none__` matches
  issues where the property is unset (works for every type; it is not
  index-backed, so use it for targeted audits rather than as a default
  listing filter). Only `=` is supported today; the `>=`, `<=` and `!=`
  spellings are reserved for comparison filters and are rejected.
- `--sort property:<name-or-id>` orders select properties by option order —
  an ordinal scale (Low < Medium < High) sorts by meaning — and number/date/
  text/url by value; issues without the property sort last either way.
  Archived properties and types without an order (multi_select, checkbox,
  actor kinds) are rejected up front.
- `issue list` and `issue get` return `properties` as a map of definition id
  to stored value. Add `--resolve-properties` in JSON mode to get the rows
  `issue property list` prints instead (name, type, stored value, display
  names); the CLI makes at most one catalog request for the whole page, so
  no `property list` call is needed:

```bash
multica issue list --status in_progress --output json --resolve-properties
multica issue get <issue-id> --resolve-properties
```

  Read `display` for a single value and `display_values` for a multi_select
  or multi_actor value; `value` keeps the stored ids.

## Status changes have server side effects

A status change is not cosmetic — the server enqueues or skips agent work based
on it. These are the contracts, not advice.

The rules below name fixed built-in status keys, not category-wide behaviors.
Custom statuses have only lifecycle semantics: unstarted, started, done
(successful terminal), or closed (cancelled terminal). They do not inherit
Backlog parking, In Review completion, Blocked failure, or In Progress recovery.
Use the built-in key when its special behavior is needed. Built-in definitions
cannot be edited or archived.

Archive a custom status only after moving every issue off it, including
completed/canceled issues. An occupied status returns HTTP 409 with code
`issue_status_in_use` and `issue_count`; it remains active. Use Settings >
View issues to inspect and move its issues, then retry. For terminal-status
replacement, preserve the lifecycle meaning (`done` to `done`, `closed` to
`closed`); do not reopen or cancel completed work just to retire a status.
Archival does not move issues automatically. Historical issues on previously
archived statuses remain readable via an explicit status filter.

- **`backlog`** parks an agent-assigned issue: the assignee is set but no task
  fires. Moving `backlog → todo` (or any non-done/non-cancelled status) enqueues
  the assigned agent then.
- **`in_progress` / `in_review`** are agent-managed CLI mutations, not automatic
  side effects of a task starting or finishing. The runtime brief asks agents to
  write the state the issue is in whenever their work changes it — not from
  the trigger type or the run's lifecycle, and not gated on being the
  assignee. Writes happen whenever the state changes, mid-turn included: a
  turn that advances the issue's own ask sets `in_progress` as soon as that
  is known, so the board shows the work while it runs; a blocker is recorded
  when it is hit; and the turn must not exit with a stale value — delivered
  the issue's own ask → `in_review`; work continues beyond the turn
  (dispatched sub-issues, partial delivery) → `in_progress`; stuck →
  `blocked`. A turn that produces none of the issue's own deliverable —
  answering a question, consulting on work owned elsewhere — writes nothing
  at any point. The kind of activity never decides this: research, design,
  planning, and review all count as the work exactly when they are what the
  issue asks for (a review-the-PR issue is being worked the moment reviewing
  starts). Questions, discussion, or acknowledgements never move the status.
  Squad leaders: dispatching members is not delivery — a dispatch turn
  leaves the parent `in_progress`, and it moves to `in_review` only when a
  later re-trigger confirms the overall goal is met.
- **`in_review`** is an accepted issue status. Some workflows use it while a PR
  is open and awaiting review; moving to it is an explicit mutation.
- **`done`** on a child issue can wake its parent's assignee (see Stages).
- **`cancelled`** is a terminal, user-driven decision to close the issue. Like
  `done` it enqueues no new agent work, but it does **not** stop tasks already in
  flight — a run in progress keeps going. To stop a running task, cancel the
  task itself.
  A cancelled issue may also be marked as a **duplicate** of another issue.
  When you cancel an issue because the work already exists elsewhere, mark it
  with `multica issue status <id> cancelled --duplicate-of <original>` rather
  than cancelling and explaining in a comment: only the mark links the two.
  The original must not itself be a duplicate, and an issue that others are
  marked as duplicates of cannot be marked; the command reports both refusals.
  (`GET /api/issues/<id>/duplicates` shows both sides; issue responses carry
  the original as `duplicate_of` with its id, identifier, title and status
  while the mark counts). Moving it to any
  status other than `cancelled` removes the mark, so reopen a duplicate only
  when it is really separate work. Marking logs `duplicate_marked` on the
  duplicate and `duplicate_added` on the original; removing the mark logs
  `duplicate_unmarked` / `duplicate_removed` (`multica issue timeline --action`).
- **Failed issue-triggered tasks** may roll an issue from `in_progress` back to
  `todo` when no active task / retry remains — that is the main server-owned
  status write on the agent-run path.

## Who else is running right now

Nothing about concurrent runs is pushed into your prompt: the answer changes
while a turn is running, and most turns never need it. Ask the server on the
turns that do — before opening a PR against code a sibling issue also touches:

```bash
multica issue runs <issue-id> --active --output json     # in-flight runs on this issue
multica issue runs <issue-id> --siblings --output json   # ...and across the sub-issue family
```

`--active` drops the execution history and returns only `queued` / `dispatched`
/ `running` / `waiting_local_directory` runs. `--siblings` widens the same read
to the issue's family — its parent (or itself, when it has no parent) plus every
child of that parent — and labels each row with the issue it belongs to, which
is how you find another agent already working on a sibling sub-issue before you
open a second PR against the same code.

The family read returns a compact row — task, issue, agent, status, started —
not the full execution-log record. If you need a run's detail, follow the task
id with `multica issue run-messages`.

Rows come back running-first, newest-first within a status, and the family read
is capped at 20. When the cap truncates the answer the CLI prints a warning on
stderr — read it. Without that warning a short list means "nobody else is
there"; with it, the list proves nothing about the runs it did not return.

Both are advisory reads. Nothing here reserves an issue or serialises anything:
a run you see may finish a second later, and one you don't see may start a
second later. Coordinate through the issue's comments — the reads tell you whom
to coordinate with.

## Rerun, cancel, and inspect a run

A run's lifecycle is read and controlled from the issue, not the daemon.

- `issue runs <issue-id>` — full execution history, newest first. `--active`
  returns only in-flight runs; `--siblings` widens to the sub-issue family (see
  "Who else is running right now"); `--full-id` prints full run UUIDs.
- `issue run-messages <run-id> [--since <n>]` — one run's messages; `--since`
  takes a sequence number.
- `issue usage <issue-id>` — aggregated token usage. In table output RUNS counts
  terminal runs, and a total prefixed with `>=` is a lower bound (one or more
  terminal runs did not report usage).
- `issue rerun <id>` — re-enqueue an issue's current agent assignment as a fresh
  run. A write: confirm the assignment is the one you actually want to rerun
  (not a stale or superseded one).
- `issue cancel-task <run-id>` — cancel an in-progress or queued run and
  interrupt the in-flight agent so it stops emitting tool calls promptly;
  `--issue <id>` scopes an ambiguous short run-id prefix. A write that stops a
  live agent — use only to abort a run you meant to stop.
- `issue search <query>` — search issues by title, description, or comments.
- `issue reorder <id>` — move an issue within its status column (changes
  position only, not state).

## Subscribers

`issue subscriber` controls who is notified about an issue — separate from the
assignee and from wakeups.

- `issue subscriber list <issue-id>` — who is subscribed.
- `issue subscriber add <issue-id>` — subscribe a member or agent (defaults to
  the caller; name or id the target).
- `issue subscriber remove <issue-id>` — unsubscribe (defaults to the caller).
- Subscribing routes notifications to the subscriber; it does **not** start a
  run. For a future run use `issue wakeup` (below), not a subscription.

## Labels

Labels tag issues and filter them; they are distinct from properties and status.

- `issue label list <issue-id>` — labels set on the issue.
- `issue label add <issue-id> <label-id>` /
  `issue label remove <issue-id> <label-id>` — add or remove a label by its
  UUID (a UUID from `label list`, never a display name — see "A name is not an
  id").

## Sub-issues: todo starts work now, backlog parks it

On an agent-assigned issue, create status decides whether the assignee fires
immediately. A non-backlog status (e.g. `todo`) enqueues the agent at create
time; `backlog` sets the assignee without triggering.

Parallel children — all start now:

```bash
multica issue create --title "..." --parent <issue-id> --assignee <agent> --status todo
```

Strictly serial children — park later steps, promote one at a time:

```bash
multica issue create --title "Step 2: ..." --parent <issue-id> --assignee <agent> --status backlog
multica issue status <child-id> todo   # promote when the previous step is truly done
```

Creating every serial step as `todo` enqueues the whole chain at once.

### Stages: order sub-issues into barrier groups

`--stage <N>` (N >= 1) groups sub-issues under the same parent into ordered
stages. The platform's sub-issue wakeup **wakes the parent assignee when a stage
closes while a later stage is waiting** — every sub-issue up to that stage has
reached a terminal status (`done`/`cancelled`) — and **once more when every
sub-issue, staged or not, is closed**. A completion that closes nothing is
silent. A sibling set with **no** stages wakes the parent once, when the *last*
sub-issue finishes. A parent in `backlog` is not woken; it catches up once it
leaves backlog. A member assignee gets an inbox notification instead of a run.

Advancement is agent-driven: the server only detects the closed barrier and
wakes the parent assignee, who then decides whether to promote the next stage's
`backlog` sub-issues to `todo`.

```bash
# Stage 1 runs now; later stages parked until promoted
multica issue create --title "Research A" --parent <id> --assignee <agent> --stage 1 --status todo
multica issue create --title "Research B" --parent <id> --assignee <agent> --stage 1 --status todo
multica issue create --title "Build"      --parent <id> --assignee <agent> --stage 2 --status backlog
multica issue create --title "Ship"       --parent <id> --assignee <agent> --stage 3 --status backlog
```

When both Stage 1 sub-issues finish you (the parent assignee) are woken by the
sub-issue wakeup; its `[WAKEUP]` block lists every stage and names the next one.
Inspect the layout, then promote the next stage:

```bash
multica issue children <parent-id>             # sub-issues grouped by stage
multica issue status <stage-2-child-id> todo   # promote when its deps are met
```

`issue children --output json` reports per-stage `done` counts, including custom
statuses in terminal categories. When reading issue JSON, `status` is the exact
key; `status_category` retains the seven-value API enum for installed clients:
`backlog` / `todo` mean unstarted, `in_progress` / `in_review` / `blocked` mean
started, `done` means successful terminal, and `cancelled` means cancelled
terminal (the internal closed category). These values encode lifecycle, not
built-in automation behavior. Check `status_category` for `done` / `cancelled`
(or use the stage counts), not just the concrete `status` key, to recognize
terminal children.

Read each sub-issue's description before promoting and only promote items whose
stated dependencies are met; if a description conflicts with the parent's
breakdown, leave it `backlog` and comment to confirm first.

## Charts and files in a comment

Where content goes decides how it shows:

- **In the body, rendered in place** — a fenced ` ```html ` or ` ```mermaid `
  block in the comment content. It renders inside the comment with a title
  bar (Preview / Source, fullscreen, copy) and takes its content's height;
  anything taller than 480px collapses behind "Show all". Name it with
  `title="..."` on the fence line. HTML runs in a scripts-only sandbox (no
  cookies, storage or parent access; CDN `<script src>` works).
- **An attached file** — `--attachment <path>`. Every non-image file shows as
  a file card that opens in the viewer, **HTML included**: an uploaded
  `report.html` is a deliverable to open, not an inline chart. Use it for
  something the reader keeps or downloads.

For HTML that should follow light / dark mode, style it with the page's theme
variables: `var(--background)`, `var(--foreground)`, `var(--muted)`,
`var(--muted-foreground)`, `var(--border)`, `var(--primary)`,
`var(--chart-1)` … `var(--chart-5)`, `var(--font-sans)`. Using any of them opts
the block into the app's color scheme, so also set the page background
(`body { background: var(--background); color: var(--foreground) }`). HTML
that uses none keeps its own look. Size to the content, not the viewport:
`100vh` heights have no fixed viewport to fill here.

````markdown
```html title="p95 latency, last 7 days"
<canvas id="c"></canvas>
<script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
<script>/* draw with getComputedStyle(document.documentElement)
  .getPropertyValue("--chart-1") so it follows the theme */</script>
```
````

## Incorrect to correct

PR title (link the issue):

```text
Fix login redirect                  # incorrect — no issue key, won't link
Body-only "Part of MUL-123"         # incorrect — passing mention, won't link
MUL-123: fix login redirect        # correct — links the PR
```

Serial / phased sub-issues (don't start the whole chain at once):

```bash
# incorrect — all fire immediately, no ordering
multica issue create --title "Step 2" --parent <issue-id> --assignee <agent> --status todo
multica issue create --title "Step 3" --parent <issue-id> --assignee <agent> --status todo

# correct — stage them; Stage 1 runs, later stages park and are promoted as
# each stage's barrier closes
multica issue create --title "Step 1" --parent <issue-id> --assignee <agent> --stage 1 --status todo
multica issue create --title "Step 2" --parent <issue-id> --assignee <agent> --stage 2 --status backlog
multica issue create --title "Step 3" --parent <issue-id> --assignee <agent> --stage 3 --status backlog
```

Reading a file you do not control (incorrect):

```bash
# incorrect — --description-file / --attachment default to the current working
# directory; a path outside it is refused unless --allow-external-file is set
multica issue update <id> --description-file /tmp/other-run/description.md
# correct — write the file inside the working directory, or pass
# --allow-external-file when the path is genuinely outside it (MUL-4252)
multica issue update <id> --description-file ./description.md
```

## Issue wakeups

For event, condition and timer rules, recurring checks and check-ins, read
[Issue wakeups](issue-wakeups.md).


## Run handoffs and member replies

After posting the final results and updating the final status of an unfinished issue, read its current revision and report the exact next work with `multica issue next-step <issue> --body-file ./next-step.json`. This is a run-scoped command: it can only write the authenticated running task's issue. Include `kind`, `summary`, `actor_type` (`member`, `agent`, or `unknown`), `actor_id` when known, `issue_revision`, and concrete `missing` fields or `evidence`. An agent actor must be the reporting agent. Never infer a reviewer or blocker from a title. Use `review` only with actual delivery evidence; ending a run does not complete its issue.

For a required decision or manual action, first create this run's formal request with `multica human-request create --body-file ./request.json`, then include its `request_id` in a `manual_action` or `awaiting_decision` handoff with the same designated member. Ordinary `choice` and `input` requests may set `response_mode: "chat_or_card"`. A composer-bound, successfully persisted reply consumes the exact request revision and is as final as the card control. Do not ask for the same confirmation again. Discussion and failed submissions do not consume requests. Authorization, manual completion, and existing card-only requests keep explicit card controls; verify manual work in the resulting follow-up before proceeding.
