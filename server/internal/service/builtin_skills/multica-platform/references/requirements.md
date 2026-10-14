# Requirements

A **requirement** is an issue with `kind=requirement`. A `kind=task` issue has no
requirement. A requirement issue carries a 1:1 requirement row, created atomically
with the issue, whose `description` **is** the requirement document, and whose
lifecycle is `pending_confirm` → `confirmed` → (change).

## Read it

```bash
# Create a requirement issue (its description is the requirement document).
multica issue create --title "Spec for X" --kind requirement --description-file ./req.md

# Read the lifecycle of a requirement issue (404 for a kind=task issue).
multica issue requirement <issue-id> --output json
```

`issue requirement` reports:

- `lifecycle_status` — `pending_confirm` or `confirmed`.
- `confirmed_revision` / `confirmed_by` / `confirmed_at` — the document revision
  the confirmation pinned (blank while `pending_confirm`).
- `unconfirmed_change` — true when the document was edited after the last
  confirmation; that edit **is** the change.

`--kind` on `issue create` is `task` (default) or `requirement`. A plain task
never gets a requirement row.

## Confirm and change — the agent drives it, the human only answers

Confirming and deciding a change are **human-request cards the agent delivers**;
the human only answers. Use `multica human-request create` (read its `--help`
for the full contract). The two keys a requirement uses:

1. **Confirm.** When the document is ready, deliver a `confirmation` card keyed
   `requirement-confirm`. The member's approval deterministically confirms the
   requirement (no agent action); the confirmed revision is the document as it was
   at confirm.

   ```
   multica human-request create --key requirement-confirm --kind confirmation \
     --title "Confirm requirement MUL-1" --action-label "Confirm" \
     --next "I will create sub-tasks after confirmation"
   ```

2. **Change.** If the member edits the document after confirmation, that edit is a
   change (`unconfirmed_change` flips true, the issue re-opens to `pending_confirm`).
   Deliver a `choice` card keyed `requirement-change-review` with choices
   `keep` / `redo` / `cancel` / `adjust`; the lead's answer re-applies the impact
   across the requirement's sub-issues and re-confirms the document.

   ```
   multica human-request create --key requirement-change-review --kind choice \
     --title "Requirement changed: impact" --action-label "Decide" \
     --choices '[{"id":"keep","label":"No impact","recommended":true},{"id":"redo","label":"Rework affected sub-tasks"},{"id":"cancel","label":"Cancel affected sub-tasks"},{"id":"adjust","label":"Flag for re-review"}]' \
     --next "I will apply the impact to the sub-tasks"
   ```

## Sub-tasks

After a requirement is confirmed, development and bug-fix work is created as
**sub-issues under the requirement issue** (a task `--parent` pointing at the
requirement). Create them once the confirmation lands; do not create them while
the requirement is still `pending_confirm`.

## Consequences

- Confirming and a `redo`/`cancel` change decision move sub-issues and re-confirm
  the document — durable, budget-costing writes. Confirm the impact with the lead
  via the card before the agent acts on it.
- Reusing a `requirement-confirm` / `requirement-change-review` key with unchanged
  content is idempotent; changing it invalidates the old confirmation.
- A `kind=task` issue has no requirement row: `issue requirement` returns 404, and
  there is nothing to confirm.
