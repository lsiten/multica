# Runtime discovery cache recheck

Verdict: **PASS for the previously reported target/repository runtime-cache blocker.** No blocking defect found in this scoped recheck. This is not whole-product approval.

## Scope and evidence

Read `packages/views/issues/components/local-review-dialog.tsx`, its focused test, and the immediate `readLocalReview` adapter. No source edits, real agents, external writes, backend/OS acceptance, or secret/config access.

CodeGraph CLI status confirmed an initialized index with pending changes; the exposed MCP status attempt rejected its input. Current source was therefore read directly for this narrow, explicitly named change.

## Findings

- **Isolation:** the identity key at lines 19–20 contains both `workspace_id` and `task_id`. It omits comparison target and repository path deliberately. Snapshot keys at line 25 retain workspace, task, path, and target. Identity writes use the matching closure key, so a completed older request cannot write into another task/workspace's identity entry.
- **Reuse:** successful snapshot reads write discovered `runtime_id` at line 30. The disabled query observes that cache entry and its data feeds `effectiveRequest` at line 21. Changing target or repository therefore creates a new snapshot query while carrying the discovered task runtime. Repository switching still clears the original review identity when appropriate.
- **Explicit identity:** `request.runtime_id || runtimeIdentity.data` gives a nonempty explicit identity precedence, including when a cache entry already exists. `initialData` seeds new cache entries; it is not an overwrite of an existing entry. Snapshot discovery subsequently refreshes the shared entry. Operations also prefer effective identity before their snapshot fallback (line 39).
- **Lifecycle:** this is a subscribed React Query observer despite `enabled: false`; it does not initiate its own fetch. Installed QueryObserver implementation adds observers on subscription and removes them on unsubscribe/destroy and query changes. The identity is retained while observed and follows normal cache garbage collection after unmount; indefinite retention is not promised.
- **No feedback loop found:** writing identity may rerender the component, but identity is absent from the snapshot key. QueryObserver's `shouldFetchOptionally` requires a changed query or previously disabled query; changing the query function closure alone does not trigger another read. Ordinary snapshot polling remains intentional. The identity query stays disabled.

## Verification and limits

Executed `pnpm --filter @multica/views test issues/components/local-review-dialog.test.tsx`: **1 file, 6 tests passed**, including `retains discovered runtime identity when changing the comparison target` (1.39 s reported duration).

That regression proves the next target read receives the discovered runtime. Repository reuse, cross-task/workspace isolation, explicit precedence, and observer cleanup were verified by source inspection, not new dedicated mounted scenarios. The test does not count cloud discovery calls directly because it mocks `readLocalReview`. No typecheck was rerun; the root's earlier broader checks are separate evidence. No live browser/Electron acceptance was performed.
