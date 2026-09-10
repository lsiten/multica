# Local review surface

## 1. Scope
Preserve the existing MR dialog and shared component system. This work changes data loading and review interaction, not the application brand or task layout.

## 2. Color
Use the shared semantic background/card/popover, foreground, muted-foreground, border, success, warning and destructive tokens. Added/removed code uses success/destructive text and tinted backgrounds; color is accompanied by diff markers and line numbers.

## 3. Typography
Use text-caption for dense metadata/code, text-body for ordinary controls, and font-mono for file paths/patch content. Do not add fonts or raw font sizes.

## 4. Layout
Keep the existing flex dialog with bounded viewport height. Its file-browser region owns scrolling. Desktop has a file-list column and diff column; smaller widths stack them. Both grid children must allow shrinking without pushing controls outside the dialog.

## 5. Primitives and states
MR entry buttons are fail-closed until the owning runtime confirms at least one Git repository. Show checking, confirmed-empty, and verification-failed states beside the disabled entry, with an explicit recheck action. Share this gate across task, agent, and worktree-management entries. Bound concurrent probes and cancel queued reads when entries disappear. A historical work_dir is a candidate, never proof of a repository.
Reuse Dialog, Button, Input, Popover and Command. Branch selection supports keyboard search. File selection uses semantic buttons with pressed state. Loading, retry, empty, binary, oversized and unsupported content are explicit states. Patch pagination uses previous/next actions and never silently appends unlimited content.

Omitted hunk context uses a full-width ghost button on the muted hunk background. Expanding loads at most 50 original lines per explicit action, preserves both line-number columns, and offers collapse. Loading and errors appear inline at that gap; they must not block other files. Collapsing unmounts the reader and cancels unfinished work. No additional motion is introduced.

Git index controls live in a separate collapsible section from the target-branch diff. Show staged and unstaged groups, per-file actions, conflicts/unsupported flags, source branch, refresh, and a commit-message field. Render at most 100 rows per group initially with explicit load-more controls. Commit requires a second confirmation naming the staged file count; mutations disable competing controls and closing the parent dialog until settled.

## 6. Interaction and motion
Use existing component focus/hover/pressed behavior. No decorative motion is added. Target selection is separate from applying a comparison. Merge requires a separate confirmation against visible source and target commit identities.

## 7. Accessibility
Give branch search, file navigation and pagination localized labels. Errors use role=alert; load/empty feedback uses role=status. Keep visible focus, readable CJK wrapping, and bounded technical details. Code lines remain selectable.

## 8. Acceptance and debt

Selective merging is separate from Git staging. Historical branch-row plus controls add to a pending-merge group with immutable source-diff preview and removal. Working-row plus controls retain real staging behavior. The pending group uses a dedicated message and confirmation naming file count, target and reviewed commit identities; changed selection invalidates confirmation. Conflicts appear as a file list and never clear pending files or publish a target commit. Successful selective publication clears selection and reports the new target commit. No whole-source merge ancestry is recorded for a file selection.

Pending merge and actual Git commit controls share one bounded, scrollable operation area; empty Git commit controls are hidden. Their messages and destinations remain distinct. Add all enumerates every immutable manifest page before updating selection and excludes real working/index paths. Remove all clears only pending merge selection, never the Git index. Bulk loading disables selection controls; failure leaves the previous selection unchanged and exposes a retryable error.
Test lazy per-file requests, correct version identity, file pagination, patch pagination, error isolation and merge confirmation. Fresh visual acceptance is still required; lack of native control permission is an evidence gap, not a visual pass. No performance claim is based only on unit tests.

The user subsequently declined actual desktop/browser operation. Do not request or perform it for this task; report visual acceptance as not performed at their request.
