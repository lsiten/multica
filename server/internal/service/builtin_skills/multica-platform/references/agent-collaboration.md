# Agent collaboration

Use Agent collaboration when one Agent needs another Agent on the same or a
different Runtime. The server resolves a target Agent to its Runtime; do not
address a Runtime directly.

```bash
multica agent collaborate delegate <issue> <agent> --content "Review the auth changes" --request-id review-1
multica agent collaborate append <run-id> --content "Check the error mapping" --request-id append-1
multica agent collaborate next <run-id> --content "Continue with the test failures" --request-id next-1
multica agent collaborate read <run-id> --view final
multica agent collaborate watch <run-id> --request-id watch-1
multica agent collaborate stop <run-id> --request-id stop-1
```

`append` names one current Run and fails if it cannot accept input. It never
silently turns into `next`. `delegate` starts a separate Run; `next` creates a
later turn. Reuse the same request ID when a network response is uncertain.
Changing the payload under the same request ID is rejected.

An accepted operation is not a completed Run. Read the target Run or use a
terminal wakeup. When waiting, finish the current Run so another Agent can use
the execution slot. A wakeup is only a scheduling hint; the durable Run,
execution identity, transcript, and terminal result are authoritative.

Runtime rebinding moves queued and deferred work. A dispatched or running Run
stays with its actual execution Runtime. Do not copy local absolute paths across
Runtimes; exchange repository and commit references or permission-checked files.
