---
name: multica-platform
description: "Use for Multica platform actions the runtime brief does not fully cover: issue and PR contracts, issue wakeups, coordination graphs, chat, charts and files in comments, mentions, agents, squads, autopilots, projects, runtimes, skill import, notifications. Not for the product code you are working on."
user-invocable: false
allowed-tools: Bash(multica *), Bash(git *), Bash(gh *)
---

# Operating Multica

Your runtime brief owns the per-turn workflow: which issue you are on, when to
comment, what status to write. This skill owns the platform contracts behind
it — what a command actually does, what the server validates, and which writes
have consequences you cannot take back.

Read the invariants below, then open the reference(s) your task actually needs
— usually one, sometimes a few. Do not read them all.

## Routing

| Open | When the task is about |
|---|---|
| `references/issues.md` | Issues: PR linking, reading a linked PR's state, custom properties, status side effects, sub-issues and stages, who else is running (runs / rerun / cancel-task / run-messages / usage / search), subscribers, labels, comment reads, charts vs attached files in a comment |
| `references/issue-wakeups.md` | Issue wakeups: events, conditions, timers, recurring checks and check-ins |
| `references/coordination.md` | The coordination graph: how a task's project, sub-issues/stages, runs, and squad (leader / members / routing) connect — read it before acting on one node in isolation |
| `references/mentions.md` | Writing a `mention://` link: which types enqueue a run, which are inert, why one silently did nothing |
| `references/notifications.md` | Personal inbox notification bots: platform credentials, workspace scope, delivery and test-send boundaries |
| `references/agents.md` | Creating, copying or debugging an agent definition: fields, secrets, MCP config, skill binding |
| `references/squads.md` | Squads: leader routing, roster, recording leader activity, why a squad did or did not run |
| `references/autopilots.md` | Autopilots: schedule / webhook / manual triggers, `create_issue` vs `run_only`, why one did not fire |
| `references/projects.md` | Projects, bounded project supervision and their durable resources (`github_repo`, `local_directory`, worktree mode) |
| `references/runtimes.md` | Runtimes, daemons, `repo checkout`, and the task CLI boundary |
| `references/chat.md` | Reading the current chat conversation: `chat history` (channel + thread list) and `chat thread [id]` (one thread's messages) |
| `references/skill-import.md` | Importing a skill into this workspace from a URL or a local archive |

Open what the task needs. A single-domain task usually needs one; a task that
crosses domains needs each domain it touches — creating a squad, assigning it an
issue, then writing a mention needs `squads.md`, `issues.md` and `mentions.md`,
and skipping one of those means acting on a contract you have not read.

What is never right is reading every reference because you are not sure. Each
reference states its own contracts in full and none depends on another, so
pick by domain and skip the rest.

## Invariants

These hold across every reference and are not repeated there.

**Match the installed CLI capability.** This skill ships with the corresponding CLI. Older builds may lack `issue wakeup` or `project supervision`; an unknown command or flag means you must check `multica version` and update the CLI. Do not improvise another write path. Project coordination also requires a daemon advertising `project-supervision-v1`.

**Read before you write.** Start with the read-only commands the reference you
opened names — most domains have a `list` and a `get` that take `--output json`
and have no side effects. Run those before any mutation. When a command's shape
is unclear, `multica <command> --help` beats guessing at flags.

**A name is not an id.** Mention links, assignment, and every `--*-id` flag take
a real UUID from the matching `list --output json`. Never type a display name
where an id belongs, and never invent a UUID: an id that is well-formed but
belongs to nothing fails in ways that read like a permission error, which sends
you debugging access when the real problem was the id.

**`--output json` writes to stdout; warnings and confirmations go to stderr.**
Do not merge them (`2>&1`) into anything that parses the output — that makes a
write which SUCCEEDED look like it failed, and invites a duplicate retry.

**Writes are real.** Creating, updating, deleting, assigning, commenting,
mentioning, triggering and status changes mutate durable workspace state or
start agent runs that cost real budget. Never run one to see what happens. When
the user has not asked for a specific mutation, propose it instead of making it.

**Status keys identify workflow states; categories describe lifecycle only.**
Custom statuses do not inherit built-in automation behavior. For status side
effects and API field meanings, read `references/issues.md`.

**Comment reads stay bounded.** Scan the threads cheaply
(`--roots-only --summary --compact`), then expand only what matters
(`--thread <thread-id> --tail 30`). Never one unbounded pull — a wide read on a
busy issue costs more than the answer is worth and still buries the reply
bodies where triggers and instructions actually live. One exception, and it is
narrower than it looks: when the per-turn message hands you a `--since` delta
read, that read is the bounded scan — the server already computed which
comments are new, so running it returns exactly those and nothing else. To page older threads use `--before` / `--before-id` (the `X-Multica-Next-Before` cursors on stderr); `--recent N` caps THREADS, not comments, and `--full` is the only way to unfold a resolved thread (root + conclusion, dropped count reported).

## When behavior looks wrong

Classify before concluding: expected behavior, a configuration problem, a
product limitation, or an actual bug. Explain what the platform currently does
rather than defending it; when the behavior is technically correct but bad for
the user, say so and propose a scoped change.

Do not silently alter routing, briefing, or trigger behavior to make a complaint
go away. Those are product contracts, and changing one without confirmation
moves the surprise to somebody else.

## Execution context

Project lead, squad membership, and project/squad bindings determine whether an agent may run a task. Agents with no narrower relationship remain workspace-scoped. Do not bypass `agent_project_scope_conflict`, `squad_project_scope_conflict`, or `agent_squad_scope_conflict` by choosing another target; inspect the project, agent, and squad scope first.
