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

Layout: Chat workspace - keep the conversation beside its selected output -> supporting-pane (https://github.com/changeroa/StyleGallery/blob/main/patterns/split-sidebar/supporting-pane.md); scroll owners: the message list, document body, and explicitly opened file drawer; anti-patterns: a permanently visible fourth column, page-level scrolling, and remounting messages when a document opens.

The workspace responds to its own width. At 56rem it can show conversation and
document side by side; below that threshold it shows one selected pane while
keeping both mounted. Files open on request in a dismissible drawer. The user
can explicitly focus the document at any width. The divider supports pointer
and keyboard resizing; a reset action restores the default proportions.

In a full-content document tab the same conversation becomes a movable overlay,
not a second chat instance. The tab strip includes the conversation and opened
resources. Dropping a resource tab in the strip promotes it to full-content view;
resource tabs can be reordered. Selecting the conversation tab restores its full
surface. The overlay can collapse to its header, move with pointer or arrow keys,
and dock back beside the document. Its maximum size is 28rem by 36rem, bounded
by the local workspace; its collapsed header is 2.5rem high. Geometry is a layout
preference scoped to the conversation. It never changes document ownership.

## 5. Components

### Applications

- **Primary action:** create an application on the overview; start or monitor it
  on its detail page.
- **Information priorities:** actionable health summary, scoped application results,
  and the progress of an operation requested by the user.
- **Required content:** service and composition totals, instance health and runtime
  counts, project/runtime/board views, search, project and runtime selection,
  application types and states, instances, configuration, orchestration, logs,
  activity, publication and all existing lifecycle controls.
- **Filtering:** URL-backed scope applies consistently to all views. Summary
  counts describe the selected project/runtime and search; selecting a summary
  applies its type or status filter. An empty result offers reset rather than
  implying that the workspace has no applications.
- **Recovery:** first use offers project creation or application creation. Errors
  stay beside the failed data with retry. A submitted operation remains visible
  through its queued, running and terminal states, including step errors and
  cancellation.
- **Primitives:** shared Button, Select, Sheet and collection states, semantic
  tokens and role typography. The existing compact desktop density is retained.
- **Left out on purpose:** zero-only summary tiles before first use, decorative
  status charts, invented completion estimates and duplicate success toasts.

Layout: Applications - keep scope and results inside the bounded workspace -> scroll-body-shell (https://github.com/changeroa/StyleGallery/blob/main/patterns/viewport-shell/scroll-body-shell.md); scroll owner: [data-application-scroll]; anti-patterns: document scrolling, clipped filter controls, and two competing result scrollers.

Layout: Application operations - inspect progress without losing the selected application -> scroll-body-shell (https://github.com/changeroa/StyleGallery/blob/main/patterns/viewport-shell/scroll-body-shell.md); scroll owner: [data-application-operations-scroll]; anti-patterns: a scrolling close control, unlabelled steps, and a second confirmation dialog hiding progress.

### Project detail

- **Purpose:** manage project tasks and resolve work that needs attention.
- **Direction:** a compact utility surface using the existing semantic palette
  and Inter/CJK font stack. Project tokens override generic design defaults.
- **Primary action:** create a task on Tasks; save changes in supervision settings.
- **Information priorities:** project identity, selected section, its work content.
- **Required content:** task views and controls, attention items and human requests,
  supervision state and diagnostics, collaboration, applications, project properties,
  and every supervision setting with save, cancel, and error recovery.
- **Structure:** one task surface supplies the header and selected section. The
  tab list stays above the content; supervision belongs to Needs attention.
- **Supervision:** disabled state shows its configuration action; enabled state
  shows current status, nonzero category filters, diagnostics, and manual check.
  Human requests appear once in the attention feed.
- **Settings:** a modal Sheet with a fixed header and footer, one scrollable form
  body, labelled inputs, retained drafts after failure, and guarded dismissal.
  Successful saving closes the sheet; cancelling restores saved values.
- **Left out on purpose:** duplicate boards, zero-count filter rows, inline
  expanded settings, and repeated human request cards.

Layout: Project detail - keep section navigation above one selected body -> scroll-body-shell; scroll owners: task view, attention feed, collaboration, applications, and project properties independently; anti-patterns: a board above the tabs, nested attention scroll containers, and page-level overflow.

Layout: Supervision settings - edit configuration without compressing project content -> supporting-pane (https://github.com/changeroa/StyleGallery/blob/main/patterns/split-sidebar/supporting-pane.md); scroll owner: the sheet form body; anti-patterns: a scrolling footer, clipped actions, and narrow-screen fields extending beyond the viewport.

### Chat workspace

- **Primary action:** send a request with its visible references.
- **Information priorities:** conversation, current execution, selected output.
- **Required content:** session navigation, messages, queue, human requests,
  composer, context references, document content, save state, history and review.
- **Left out on purpose:** decorative metrics, repeated help text, and nested
  utility sidebars open at the same time.
- **Primitives:** existing Button and IconButton for toolbar controls, existing
  Sheet for the file drawer, and ResizablePanelGroup for the conversation split.
- **States:** no document, parallel document, focused document, compact chat,
  compact document, files open, loading, offline, and unsaved draft.
- **Persistence:** layout preferences are keyed by workspace and conversation;
  server data remains in Query. Upcoming-turn execution settings are shared
  between full-page and floating composers and survive rejected submissions.
- **Accessibility:** named panes, labelled keyboard-operable separator, visible
  focus, and button alternatives to resize and focus actions.

### Voice input

- **Purpose:** dictate task instructions into an editable draft before sending.
- **Direction:** compact utility controls using the existing semantic palette,
  role typography, and application font stack; these override generic UI defaults.
- **Primary action:** Done while recording; Send only after transcription finishes.
- **Information priorities:** recognized text, capture state, elapsed/remaining time.
- **Required content:** start, cancel, pause/resume, Done, microphone readiness,
  transcription progress, final-ten-second warning, and actionable errors/retry.
- **Recovery:** backgrounding ends capture and retains its result; failed batch
  transcription retains audio in memory for retry until dismissal or scope change.
- **Keyboard:** composer-scoped Mod+Shift+M starts/finishes; Esc cancels.
- **Left out on purpose:** direct send while recording, decorative waveforms,
  repeated guidance, and automatic sending after any voice action.

Layout: Voice input - edit text above a fixed compact action row -> cluster;
scroll owner: the existing composer editor; anti-patterns: a new modal, overlapping
editor text with status, and squeezing recovery copy into a single truncated line.

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
  Filename search pauses after 20 directory pages and exposes explicit
  continuation. A paused scan is a partial result, never an empty completed search.
  Content search uses the same authorized directory and supported text formats.
  Each explicit batch reads at most four directory pages and eight text files,
  limited to the first 64 KiB per file and three matching lines per file. Show
  the scanned count, unreadable files and truncated coverage; retry preserves
  existing results until a fresh scan is explicitly requested.
  Opening a content match reveals the exact source offset and offers Preview.
  A changed source must show an unavailable-location notice rather than jumping
  to a different occurrence. Search locations are ephemeral, separate from saved
  document identities, drafts and reading positions.
- **Accessibility:** buttons expose `aria-expanded`, file rows are keyboard
  reachable, and search has a labelled input.
- **Runtime mutation contract:** member-owned run directories support explicit
  file/folder creation, move/rename, recoverable deletion and restore. Project
  resource directories remain read-only. File manager mutations use the server
  relay and the owning managed environment service. Wait for a matching operation receipt
  before changing tabs, references or drafts. The runtime journal preserves
  deleted entries outside the checkout, never overwrites an existing destination,
  and pages recoverable entries. Repeated completed requests return their receipt;
  an interrupted prepared operation requires reconciliation instead of reapplying.
  Unconfirmed effects retain their admission scope: overlapping mutations wait
  while independent directories remain available.
- **Creation controls:** show New only when both the server and selected run
  directory advertise support. A labelled dialog selects file/folder and a
  relative path; parent folders must already exist. Creation never overwrites.
  Keep the dialog during submission, then refresh the directory and open a
  successfully created file. A confirmed no-effect refusal keeps the name
  editable. A lost response keeps the exact command and payload across reloads;
  Resume creation retrieves its receipt before another creation in that directory.
  Other directories remain independently usable. Project resources expose no
  creation control.
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

### Message actions

Settled messages expose whole-message quotation and a new-discussion action.
Quotes retain their source conversation, message, author, timestamp, attachment
identities and original text. Source actions reopen the authorized conversation
and reveal the original message; fenced snapshot content cannot introduce extra
source controls. A new discussion opens an editable direct-chat draft with the
same agent. Its prompt explains that project context, history, files and the
source conversation's directory binding are not inherited. Sending remains a
separate user action. Both the original draft and existing new-chat references
survive this transition, and a quoted new-chat draft reopens after reload.

Layout: Chat message actions - settled reply above a compact action row -> cluster;
scroll owner: the existing conversation list; anti-patterns: automatic sending,
silently reusing a prior execution directory, and replacing an unfinished draft.

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
