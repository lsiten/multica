# Chat

Reading a chat conversation from the CLI — and sending one.

`multica chat` reads the conversation the run is in. The read commands
(`chat history`, `chat thread`) are read-only: they return messages and the
thread list and mutate nothing. `chat send` is the one write — it posts a
message and woken the session's agent (see below).

## Overview vs a thread

- `multica chat history` — the channel this conversation is in: the message
  overview plus the thread list. Use it to see the shape of the conversation
  before deciding which thread to open.
- `multica chat thread [id]` — one thread's messages. With no id it reads the
  current thread; with an id it reads that thread.

## Bounded reads

Both take the same paging flags:

- `--limit <n>` — maximum number of messages to return (the server clamps the
  range).
- `--before <cursor>` — opaque cursor (a `next_cursor` from a prior page) to read
  older messages.
- `--output json` — JSON output (the default here; the table format is also
  accepted).

Read `chat history` to triage which thread matters, then open only that thread
with `chat thread <id>` — the same "bounded scan, then expand" discipline as
issue comment reads. A read never enqueues a run or changes state; to post a
message (and act on @-mentions in a group), use `chat send`.

## Group chat

A group chat is a `kind=group` session that hosts several human participants
plus one session agent. It is created with the CLI; the read commands above
(`chat history`, `chat thread`) do not touch it.

A group is created **standalone** (`is_project_default=false`): it can be
archived or deleted like an ordinary chat. A project's **default** group is
created automatically when the project gets a lead, cannot be archived manually,
and only archives when the project reaches a terminal status — you do not create
that one, and these commands never target it.

```bash
# Create a group. The project is optional — without --project the group is not
# bound to any project. The calling user is always added as a member.
multica chat group create --agent <name-or-id> [--title <t>] [--project <name-or-id>]

# Manage participants of an existing group (add the rest after create).
multica chat group members list <session-id> --output json
multica chat group members add <session-id> --member-type member --member <member-uuid>
multica chat group members add <session-id> --member-type agent --member <agent-name-or-id>
multica chat group members remove <session-id> member <member-uuid>
```

- `--agent` is the session agent (name or id, resolved like any other agent
  reference). `--project` is optional; omit it for a group with no project.
- The creator is always a member participant; `members add` adds the rest. A
  `--member-type agent` reference resolves by name or id; a `--member-type member`
  reference is a member UUID (there is no member name resolver here, so read the
  member id from a `... --output json` list).
- A participant is `(member_type, member_id)`; `remove` takes the type and id.
  Adding a member already present is a no-op (deduplicated by the server).
- These are writes: creating a group starts an agent and `members add/remove`
  mutate the roster. Confirm the mutation before running it, and never retry a
  create that already returned a session.

## Sending a message

`multica chat send` posts a message to a chat session and woken the session's
agent (a real run). In a **group chat** it also acts on @-mentions in the
content; in a 1:1 chat @-mentions are ignored (there is no one else to address).

```bash
# Send a message; the session's agent is woken on every send.
multica chat send <session-id> --content "hello" --output json

# A long / multi-line message (with mention markdown) from a file.
multica chat send <session-id> --content-file ./msg.md

# @-mentions are written as [Label](mention://type/id):
#   [@all](mention://all/all)                 -> notifies every human participant
#   [@Bob](mention://member/<member-uuid>)    -> notifies that member
```

- In a **group chat**, `@all` notifies every human participant (never the sender)
  and `@<member>` notifies that member — but only when the member is a
  participant of the group; @-ing someone outside the group is a no-op.
- The session's agent is woken on **every** send regardless of mentions, so an
  `@<agent>` mention that names the main agent is just the default run, and a
  plain message with no mention still woken the agent.
- `--content` / `--content-file` / `--content-stdin` are mutually exclusive; the
  `--content-file` path must be inside the working directory unless
  `--allow-external-file` is set.
- This is a write: it starts an agent run (real budget) and may notify members.
  Confirm the send before running it, and never retry one that already returned
  a message.
