# Multica Design System

## 1. Atmosphere & Identity

Multica is a quiet command surface for people and agents working together. The
signature is a light, compact shell with tonal panels and clear focus on the
current task, file, or conversation.

## 2. Color

Use the semantic tokens in `packages/ui/styles/tokens.css`: `background`,
`foreground`, `muted`, `muted-foreground`, `border`, `surface-selected`,
`primary`, `success`, `warning`, and `destructive`. Do not add raw colors in
feature components. Selection uses a tonal wash and an icon, never an accent
border.

## 3. Typography

Use the role tokens `text-micro`, `text-caption`, `text-label`, `text-body`,
`text-body-lg`, `text-title-sm`, `text-title`, `text-title-lg`, and
`text-display-sm`. File contents use `font-mono` with `text-caption` for dense
source readability.

## 4. Spacing & Layout

Spacing follows the existing 4px rhythm. Chat is a bounded `list-detail` shell:
the thread list, detail surface, and file manager each own their own scroll
body. Headers and toolbars remain fixed; flex children that scroll always use
`min-h-0`.

## 5. Components

### File manager

- **Structure:** search field, tab strip, project root, recursive directory rows,
  file rows, and an empty state.
- **States:** collapsed/expanded directories, filtered results, selected file,
  loading, and no match. Read immediate children in pages and fetch child
  directories only when expanded; empty directories remain visible. A failed
  child read exposes its own retry while sibling directories stay usable.
  Browsing fetches subsequent pages on request. Search scans folders and pages
  progressively, displays matches as they arrive, and permits collapsing any
  matching ancestor. Changing the search or root cancels the previous scan;
  errors retain matches and offer continuation from the failed page.
- **Accessibility:** buttons expose `aria-expanded`, file rows are keyboard
  reachable, and search has a labelled input.
- **Directory selection:** include the project's bound local directories and
  distinct run worktrees, grouped as project resources, current-chat directories,
  and other task directories. Other task directories start collapsed and expand
  in batches of 20; the selected directory remains available when folded.
  Archived chats, settled issues, cleaned directories, and project supervision
  control directories do not populate this list. Use the shared Select when multiple roots exist;
  show full paths in its scrollable options and truncate the closed trigger.
  A failed or offline root stays selected with a retry action. Bound directories
  without a run are read-only; run worktrees retain editing and Git actions.

### Document surface

- **Structure:** fixed file header, scrollable content body, and a fixed action
  footer when editing.
- **Variants:** rendered document, raw editor, workspace source editor, and
  selection actions.
- **States:** loading, error, dirty, saving, conflict, and working.

### Selection actions

Selected source text exposes two actions: add the excerpt to the current chat,
or ask the active agent to edit that excerpt. The latter sends a bounded file
path and selection context and leaves the resulting change for user review.

## 6. Motion & Interaction

Use the existing 100–300ms control transitions. Directory expansion is a state
change with no layout animation requirement; reduced motion remains equivalent.
Async actions disable their control immediately and show the existing spinner.

## 7. Depth & Surface

Use the existing mixed strategy: one-pixel semantic dividers for pane boundaries
and tonal selected/hover states for rows. Avoid nested card chrome in the file
tree.

## 8. Accessibility Constraints & Accepted Debt

Target WCAG 2.2 AA, keyboard reachability, visible focus, and readable source
contrast. Accepted debt: the existing raw editor component owns syntax highlighting
and selection rendering; the file manager keeps its current compact density to
match the surrounding desktop shell.
