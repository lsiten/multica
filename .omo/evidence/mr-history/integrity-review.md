# MR history: design-system and functional integrity

VERDICT: PASS
CONFIDENCE: HIGH within the bounded fixture surface

The production history renderer maps both `merge` and `merge_recovered` to the shared `local_review.merged` translation, which is `已合并`. Both completed entries are visible and readable after expanding history at every requested width. No blocking product or evidence findings were found for this correction.

## Coverage and evidence

Directly opened and inspected all six PNGs: `375-closed.png`, `375-open.png`, `768-closed.png`, `768-open.png`, `1280-closed.png`, and `1280-open.png`, each 900px high. Inspected `results.json`, `scripts/qa/local-mr/history-check.mjs`, `scripts/qa/local-mr/main.tsx`, the production dialog, Chinese translations, shared Dialog/Button/Input primitives, and `packages/ui/styles/tokens.css`.

| Width | Closed / open | Completed entries | Diff ratio | Dimensions / alpha |
|---|---|---|---|---|
| 375 | PASS / PASS | 2 | 0.032 | true / true |
| 768 | PASS / PASS | 2 | 0.0521 | true / true |
| 1280 | PASS / PASS | 2 | 0.0498 | true / true |

Capture modification times follow both the production component and fixture source modification times. The capture script waits for fonts and completed animations, checks PNG signatures and viewport dimensions, and asserts two matching completed-event labels after the real disclosure click. Source and screenshot evidence agree. This review did not rerun the capture script or overwrite captures.

## Integrity findings

- Real UI: `packages/views/issues/components/local-review-dialog.tsx` imports shared Base UI-backed Dialog/Button and shared Input. History is a native `details`/`summary` disclosure containing an `ol`, `li`, text paragraphs, and `time` elements generated from `data.history`. No screenshot, raster substitute, or background image stands in for live controls.
- Tokens: history uses `text-caption`, shared spacing, border and radius utilities; caption resolves to the shared 12px/16px role scale. Dialog surface, text, shadow and focus treatment come from shared primitives. The correction changes event semantics without introducing a parallel visual system.
- Functionality: the fixture imports the production component and passes synthetic prior-round `merge` and `merge_recovered` events through its local API adapter. The production mapping explicitly routes both to `merged`, rather than the submission fallback. `packages/views/locales/zh-Hans/issues.json` supplies `已合并`. Current review state remains open in this fixture, so the separate `提交 MR` action button is intentional and is not a mislabeled history event.
- Responsive behavior: 375px stacks file navigation above the diff; 768px and 1280px use two columns. Expansion consumes vertical space by shrinking the flexible diff region; history, comment input, and action row remain within the dialog. Both event labels and timestamps fit at the narrow width. The diff has its own overflow container.
- Capture comparison: these are collapsed versus expanded states, not old versus new implementations. Changes in the history region, shorter diff region, disclosure marker, and loss of initial input focus/datalist affordance explain the differences. The fixed dialog bounds and footer position are retained. No unexpected opaque/black compositor regions were visible.
- Motion: shared opening/focus behavior serves the dialog interaction; no decorative motion was added by this correction.

FINDINGS: none blocking.

BLOCKING: none.

## Limits

This PASS covers only the six requested history disclosure states in the isolated synthetic fixture. It is not authenticated backend/runtime acceptance or a signoff of the entire MR feature, merging safety, other workflows, themes, or unenumerated viewport heights. Keyboard interaction was traced through native disclosure semantics rather than independently driven in this pass. CodeGraph was unavailable in the active tool surface, so source verification used bounded direct reads.
