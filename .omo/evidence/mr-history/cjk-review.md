# MR history visual/CJK review

VERDICT: PASS

CONFIDENCE: HIGH for the six supplied fixture states.

Both history events display `已合并` legibly at 375, 768, and 1280 pixels wide. No blocking CJK, history readability, wrapping, clipping, or responsive containment defect was found in this scope.

## Coverage and source

Directly opened all six PNGs with the image viewer: `375-closed.png`, `375-open.png`, `768-closed.png`, `768-open.png`, `1280-closed.png`, and `1280-open.png`, each 900 pixels high. Read all fields and every hotspot in `results.json`, plus `packages/views/issues/components/local-review-dialog.tsx`. The source maps both `merge` and `merge_recovered` to `local_review.merged`; the open screenshots show two complete `已合并` labels and both synthetic `Completed review round` comments.

The screenshots and JSON are timestamped 00:57:03–00:57:05, after the source timestamp 00:52:46. All dimension matches and alpha checks are true. The frames show complete dialog compositing without missing or black regions. CodeGraph tools were not available on this reviewer's tool surface; inspection stayed within the supplied source and evidence paths.

## Results and hotspot trace

The comparison is collapsed versus expanded history, not a same-state pixel baseline. The diff CLI counts any RGB-channel difference, even one level; its scores are locating aids rather than layout pass thresholds.

| Width | Pixels changed / total | Ratio / score | Hotspots |
| --- | --- | --- | --- |
| 375 | 10,791 / 337,500 | 0.032 / 97 | 30 |
| 768 | 36,009 / 691,200 | 0.0521 / 95 | 44 |
| 1280 | 57,369 / 1,152,000 | 0.0498 / 95 | 48 |

Every reported hotspot is covered below. Coordinates use the JSON's zero-based grid; the row bands are y=0–112, 112–225, 225–337, 337–450, 450–562, 562–675, and 675–787 respectively. Cell widths are approximately 47, 96, and 160 pixels for the three viewports.

| Width | Hotspot grid cells | Observed cause |
| --- | --- | --- |
| 375 | y1: x2–7 | Target input loses its initial focus ring/native datalist affordance after history is clicked. Text remains unchanged and contained. |
| 375 | y4: x0–7 | Diff pane bottom moves upward to provide the expanded history space; borders and the local-commits disclosure move accordingly. |
| 375 | y5: x0–7 | History disclosure moves upward and first/second event cards occupy the formerly blank pane area. |
| 375 | y6: x0–7 | Second card replaces the old disclosure/blank area; the comment field and footer remain contained. |
| 768 | y1: x0–7 | Input focus changes dominate x1–3; other cells contain minor border/raster differences. Header geometry and copy remain stable. |
| 768 | y2–3: x0, x3–7 | Right-hand diff color bands contain tiny RGB differences despite stable geometry; x0 includes the dialog/pane edge. No displaced or missing code text. |
| 768 | y4–6: x0–7 | Pane contracts vertically, disclosures move, and two history cards appear. Largest text changes are at the left; full-width border changes account for right-side cells. |
| 1280 | y0: x0, x7 | Very small dialog corner/close-region raster differences; corners and close affordance remain intact. |
| 1280 | y1: x0–7 | Target input focus changes at x1–2; minor header border/raster differences elsewhere. |
| 1280 | y2–3: x0, x2–7 | Stable code pane with small highlight-color channel changes plus edge pixels. Direct decoded samples: (800,320) changes RGB (252,229,230) to (253,229,231); (800,340) and (800,350) change (232,243,233) to (232,243,234). These explain the numerous exact-pixel differences across flat bands without a visible layout defect. |
| 1280 | y4–6: x0–7 | The pane bottom rises and disclosures/history cards replace its lower area. Text differences cluster left; card/pane borders span the remaining columns. |

## Visual assessment

- All six states: Chinese headings, selected filename `确认与合并.ts`, disclosures, comment placeholder, and action labels are complete, aligned, and free of tofu or clipped baselines.
- All three open states: each actor/status/time line fits on one line, and each comment fits on its own following line. `已合并` is never split or clipped. Both cards are visible in the bounded history list.
- At 375 pixels the file list stacks over the diff pane, the toolbar wraps, and the action row remains inside the dialog. Code extending beyond the right pane edge is consistent with the source's `overflow-auto` container and `pre.min-w-max`; it does not cause page overflow. This pass inspects the intended containment, not a performed horizontal-scroll gesture.
- At 768 and 1280 pixels the file list and code pane sit side by side. Opening history reduces the pane height while preserving dialog bounds, header positions, comment input, and footer positions. No overlap or unexpected history-driven width growth is visible.

FINDINGS: None requiring a product or evidence correction within the stated scope.

BLOCKING: Empty.

This is a visual/CJK approval of the supplied synthetic fixture and the history label mapping only. It does not certify real-account history, backend merge operations, unrelated MR interactions, long-history scrolling, or the full application. No source edits, service actions, or account access were performed.
