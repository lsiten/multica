# Runtime-global Multica CLI Skill

Install this only after `multica auth status` succeeds and the intended
Workspace is visible. Its purpose is to let the user's external local agent
recognize a pasted Multica URL and operate the resource through the
authenticated CLI in later sessions.

## Distinguish the two Skill scopes

- **Runtime-global/personal Skill:** loaded by the user's external Codex, Claude
  Code, Cursor, or similar harness. This is the scope requested by onboarding.
- **Multica Workspace Skill:** stored in Multica and injected only into Agents it
  is bound to during managed tasks.

Installing the personal Skill does not create a Workspace Skill. Conversely,
`multica skill import` does not install a personal/global Skill into the user's
external agent harness.

Codex tasks launched by Multica use a task-local `CODEX_HOME`; a personal Codex
Skill is intended for the user's external Codex sessions. Bind/import a
Workspace Skill separately if Multica-managed Agents also need the same rules.

## Bundled source

The archive includes `assets/multica-cli/`, a trusted snapshot of the official
`multica-ai/multica-cli` Skill. Copy the complete directory, including
`agents/openai.yaml` and `LICENSE`; do not copy only `SKILL.md`.

## Choose the personal Skill directory

Use the current harness's documented global/personal Skill location:

- Codex: `${CODEX_HOME:-$HOME/.codex}/skills/multica-cli/` (`CODEX_HOME` when
  set, otherwise `~/.codex`).
- Claude Code: `~/.claude/skills/multica-cli/`.
- Cursor: `~/.cursor/skills/multica-cli/`.
- Another harness: discover its documented personal Skill directory; do not
  guess or write into a project repository as a substitute.

Before writing:

1. Resolve the exact source and destination paths.
2. If the destination exists, read its frontmatter and origin. Show the
   difference and ask whether to preserve or replace it; never overwrite by
   default.
3. Copy the whole `assets/multica-cli/` directory atomically where practical.
4. Confirm the installed `name` remains `multica-cli` and the description
   recognizes pasted Multica URLs.
5. Restart or reload the external agent harness so it rescans Skill metadata.

Never copy `~/.multica/config.json`, a PAT, or any login state into the Skill
directory. The Skill calls the CLI; the CLI independently loads its protected
profile configuration.

## URL routing contract

Recognize a pasted URL under the deployment's App base:

```text
<APP_URL>/<workspace-slug>/...
```

Resolve `<APP_URL>` from `multica config show` (the `app_url` field) so the Skill
targets the user's deployment rather than a hardcoded host. For Issue routes:

```text
<APP_URL>/<workspace-slug>/issues
<APP_URL>/<workspace-slug>/issues/<issue-identifier-or-uuid>
```

Resolve the Workspace slug first:

```bash
multica workspace get <workspace-slug> --output json
```

Take the returned UUID and use it explicitly. Do not pass the slug directly to
`--workspace-id`; current CLI versions reject that shape.

For an Issue list URL:

```bash
multica --workspace-id <resolved-workspace-uuid> issue list --output json
```

For an Issue detail URL:

```bash
multica --workspace-id <resolved-workspace-uuid> issue get <issue-identifier-or-uuid> --output json
```

Strip query/fragment data before identifying the Issue path. If a link names a
comment or thread, read the Issue first, then use the comment namespace after
confirming the exact CLI flags with `--help`.

The Skill supports both reading and writing. Reads may proceed after
authentication and Workspace resolution. Creating and updating Issues,
commenting, changing status, assigning, mentioning, and rerunning are the
intended day-to-day use — this is how the user should work with Multica instead
of clicking through the Web UI — but each write happens only when the user asks
for it, and the agent confirms the target Workspace and Issue first.

## Completion check

After reloading the harness, have the user paste a link of this shape into a new
session:

```text
<APP_URL>/<workspace-slug>/issues
```

Success means the agent invokes `multica`, resolves the Workspace slug to its
current Workspace UUID, performs only the requested read, and does not ask the
user for a Token. Afterwards the user can ask the same session to create an
Issue or add a comment, and it will do so through the CLI. If `multica auth
status` fails, return to browser login rather than embedding credentials in the
Skill.
