# Chat

Reading the current chat conversation from the CLI.

`multica chat` reads the conversation the run is in. It is a read-only surface:
it returns messages and the thread list, it does not post or mutate anything.

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
issue comment reads. A read never enqueues a run or changes state; to act, use
the issue, squad, or mention commands for that.
