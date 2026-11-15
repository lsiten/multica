# Runtime surfaces

## 1. Scope

Runtime pages preserve the shared Multica dashboard shell. Mirror is a fixed
runtime sub-surface, not a workspace navigation destination or a resource list.

## 2. Color

Use only shared semantic tokens: `background`, `card`, `muted`, `foreground`,
`muted-foreground`, `border`, `ring`, `success`, `warning`, and `destructive`.
The screen frame is neutral; connection state is communicated by text and an
icon in addition to colour.

## 3. Typography

Use `text-title-sm` for the runtime name, `text-body` for controls and state,
`text-caption` for metadata, and `font-mono text-caption` for connection
identifiers. Do not introduce new fonts or raw font sizes.

## 4. Spacing and layout

Spacing follows the shared 4px scale. Runtime detail owns its single vertical
scroll container. A mirror page behaves as a full-window workbench: the session
bar fixes status, source, quality, inspector, and fullscreen controls; the
canvas owns the remaining height. Session details move into an Inspector rather
than stacking above the media. At narrow widths the Inspector becomes a bottom
sheet and the page never creates horizontal scrolling for primary content.

## 5. Components and states

### Runtime mirror entry

- **Structure:** a semantic link/button in runtime and bound-agent detail.
- **States:** available, runtime offline, unsupported client, connecting,
  connected, failed.
- **Accessibility:** visible focus, an explicit accessible name, and status
  text rather than colour-only availability.

### Mirror frame

- **Structure:** labelled media canvas with an overlaid control dock, source
  popover, and one primary live-status announcement.
- **States:** permission check, daemon connection, session creation, ICE,
  decoding, streaming, reconnecting, and terminal failure.
- **Interaction:** Fit, 100%, Fill, zoom, pan, fullscreen, automatic quality,
  diagnostics, and an explicit human-control request live outside the media
  content.
- **Accessibility:** the frame has a text alternative describing the live
  runtime source; state changes use one `role=status`; errors use
  `role=alert`.

### Human takeover

- **Structure:** control state, current controller, lease expiry, keyboard
  capture, clipboard direction, pointer mode, text fallback, AI handoff, and
  emergency stop live in the Inspector.
- **States:** view only, waiting for approval, approval pending, controlling,
  another controller active, and ended.
- **Authority:** runtime owners and workspace admins may approve a request or
  force-end another controller's lease.
- **Execution:** every pointer, keyboard, text, and clipboard action is sent
  through the approved lease to `agent-desktop`. The browser never applies
  desktop input directly.

### Hold-to-talk voice composer

- **Structure:** a full-screen dimmer contains an anchored voice card while
  recording; the card follows the active pointer and stays one 16px spacing
  token above it. A live transcript, when available, sits above the card.
  The measured card bounds are clamped to the viewport; if there is not enough
  room above the pointer, it flips below it to remain visible.
- **States:** requesting microphone, recording, cancellation-by-slide, and
  transcribing. Transcript updates are announced politely and remain visible
  during the active gesture.
- **Interaction:** pointer coordinates and measured overlay bounds are clamped
  to a 16px viewport inset; keyboard activation uses a centered fallback
  anchor. Position updates use a compositor transform and no decorative motion
  is added. Reduced-motion users keep the same anchored layout without the
  pulse animation.
- **Accessibility:** the transcript uses `aria-live="polite"`; the existing
  status and error labels remain the primary instructions and recovery path.

## 6. Interaction and motion

Reuse existing button/link hover, active, and focus behaviour. No decorative
motion is added. A still frame is replaced only when a complete newer frame is
received, so partial network data never flashes on screen.

Touch tap clicks, one-finger movement scrolls the remote surface, and a
400 ms press followed by movement starts a drag. Two fingers pan and pinch the
local video viewport. Mouse and pen keep immediate press/drag. Esc always
releases the active control lease.

## 7. Depth and surface

Use the repository's existing border-separated card surfaces. Do not add a
second elevation or shadow system for mirror media.

## 8. Composer states

The AI Inspector can create a chat bound to the exact current runtime source so
a selected agent receives an instruction with the same source identity.
Sending reports a localized error and preserves the draft for retry; Enter does
not submit during IME composition. Human input remains unavailable until a
runtime owner or administrator approves a control lease.

## 9. Accessibility and accepted debt

Target WCAG 2.2 AA: all controls are keyboard reachable, focus remains
visible, and state text has sufficient contrast. Native capture supports
physical and system displays; application-window and audio sources are
intentionally out of scope. No decorative or simulated screen animation is
used, including reduced motion.
