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
scroll container. A mirror page uses the existing page shell: breadcrumb and
actions are fixed by the shell; the media frame is the only scrollable body
region and keeps a bounded aspect ratio. At narrow widths it remains one
column and never creates horizontal scrolling for primary content.

## 5. Components and states

### Runtime mirror entry

- **Structure:** a semantic link/button in runtime and bound-agent detail.
- **States:** available, runtime offline, unsupported client, connecting,
  connected, failed.
- **Accessibility:** visible focus, an explicit accessible name, and status
  text rather than colour-only availability.

### Mirror frame

- **Structure:** labelled media region with a bounded image canvas and a
  connection-status region.
- **States:** idle, negotiating, streaming, unavailable, capture permission
  denied, and transport failure.
- **Accessibility:** the frame has a text alternative describing the live
  runtime source; state changes use `role=status` or `role=alert`.

## 6. Interaction and motion

Reuse existing button/link hover, active, and focus behaviour. No decorative
motion is added. A still frame is replaced only when a complete newer frame is
received, so partial network data never flashes on screen.

## 7. Depth and surface

Use the repository's existing border-separated card surfaces. Do not add a
second elevation or shadow system for mirror media.

## 8. Accessibility and accepted debt

Target WCAG 2.2 AA: all controls are keyboard reachable, focus remains
visible, and state text has sufficient contrast. Initial native capture uses
the primary display; source selection and audio are intentionally out of scope
for this first capability.
