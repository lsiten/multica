# History label visual QA: GOOD (scoped)

Intent: completed `merge` and `merge_recovered` events show 已合并, not requested
or submitted. Two regression cases failed before the fix; five dialog tests and
views typecheck passed afterwards.

| Dimension | Pass | Verdict | Evidence |
| --- | --- | --- | --- |
| Real DOM/shared primitives | A | good | integrity-review.md; production dialog |
| Completed history semantics | A+B | good | two labels in every open capture |
| Responsive containment | A+B | good | 375/768/1280 × 900 closed/open |
| PNG/alpha | A+B | good | results.json, valid dimensions and alpha |
| CJK readability | B | good | cjk-review.md; no blockers |

Six fresh captures were checked by both independent reviewers. Image differences
compare closed/open disclosure states, not an old/new pixel baseline. Initial
captures were premature during entrance rendering; they were regenerated after
waiting for fonts and animations before reviewer dispatch.

Both reviewers returned PASS with no blockers. This gate covers only the history
label/disclosure delta in the synthetic fixture, not the full MR/backend/login
experience. No product styling changes were made for this fix.
