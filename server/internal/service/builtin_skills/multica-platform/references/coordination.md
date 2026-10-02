# Coordination graph

How work and collaboration are actually connected — the map an agent reads
before acting, so it does not act on a single node in isolation.

Two graphs share the same issues:

- **Work graph** — `project → issue → (parent / stage) sub-issues → runs`.
- **Collaboration graph** — `squad → leader / members → issue → comment / mention / activity`.

This is the navigation, not the contracts. The detailed contracts live in the
references this page links to; read them when you must, pick by domain, and do
not read every reference because you are unsure.

## Where a task sits (read before you act)

Answer these cheaply, then expand only what matters:

```bash
multica issue get <issue-id> --output json          # its project, assignee, status
multica issue children <issue-id> --output json     # sub-issues grouped by stage
multica issue runs <issue-id> --siblings --output json  # who is working alongside you
```

`--siblings` widens the in-flight read to the issue's family — its parent (or
itself when it has no parent) plus every child of that parent. Use it before
opening a PR or starting overlapping code against the same repo. It is
advisory: it reports work in flight, it does not reserve or serialise anything
(see `references/issues.md`, "Who else is running right now").

## Work graph: project → issues → sub-issues

- A project groups issues and carries durable resources; its `description` is
  injected into every bound task's brief as `## Project Context`. See
  `references/projects.md`.
- Bind an issue to a project: `multica issue create --project <project-id>`.
- Order a chain of steps as sub-issues:

```bash
multica issue create --title "Step 1" --parent <issue-id> --stage 1 --status todo
multica issue create --title "Step 2" --parent <issue-id> --stage 2 --status backlog
```

`--stage N` groups sub-issues under the same parent into ordered barriers; the
parent assignee is woken when a stage closes and once more when every
sub-issue, staged or not, is closed. `backlog` parks a step without starting
it; promote it with `multica issue status <child-id> todo` when its
dependencies are met. Full contract: `references/issues.md`, "Sub-issues" and
"Stages".

## Collaboration graph: squad → leader / members → issue

A squad is a leader plus members; assigning or mentioning a squad routes to the
**leader only**, never a member fan-out.

```bash
# assign work to a squad (routes to its leader); --no-start assigns ownership without starting a run
multica issue assign <issue-id> --to <squad-name> --no-start --output json
# or by UUID
multica issue assign <issue-id> --to-id <squad-id> --output json

# roster
multica squad member list <squad-id> --output json
multica squad member add <squad-id> --member-id <id> --type agent --role <role> --output json
```

- A comment on a squad-assigned issue wakes the leader, not the members; a
  worker's reply re-wakes the assigned leader without an explicit mention.
- Hand work to a squad by mention: `[@Name](mention://squad/<squad-id>)`
  resolves the squad's `leader_id` and enqueues a leader run, using the current
  comment as the trigger. See `references/mentions.md` and `references/squads.md`,
  "Comment and mention behavior".
- The leader records its evaluation on the issue it is running:

```bash
multica squad activity <issue-id> action|no_action|failed --reason "<why>" --output json
```

Status authority is granted only when the issue's `assignee_type` /
`assignee_id` point at **this** squad; on an `@squad` mention on an
agent-owned issue the protocol instead carries "do not change this issue's
status". See `references/squads.md`, "Issue assignment behavior".

## Reading the graph, not just one node

- A squad-assigned issue can also live in a project; read both — the project
  resources and `## Project Context` shape the run, the squad routes it.
- Before starting overlapping work, check `issue runs --siblings`; before
  promoting a stage, read the sub-issue descriptions and confirm dependencies
  (`issue children`, `references/issues.md`).
- A failed or idle run does not mean the goal is met; read the graph (parent,
  stages, siblings) before declaring done.

## When to read what

| You need | Read |
|---|---|
| the project and its resources | `references/projects.md` |
| issue status, PR linking, comments, stages | `references/issues.md` |
| who a squad routes to, roster, activity | `references/squads.md` |
| a `@squad` / `@agent` mention that enqueues | `references/mentions.md` |
| a scheduled or triggered run | `references/autopilots.md` |

## Execution scope

Project leads are implicitly allowed to run their project. Agent members inherit active project bindings from their squads. An agent with no project-lead, agent-project, or squad-project relationship remains workspace-scoped. Empty binding lists therefore mean workspace scope, not no access. Scope conflicts are rejected at every enqueue path and must be investigated by reading the project, agent, and squad first; do not retry with an arbitrary target.
