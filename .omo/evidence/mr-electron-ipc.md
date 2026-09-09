# Isolated Electron IPC acceptance

Result: PASS for the renderer → production preload → real Electron IPC → main
request function → localhost HTTP contract. Not a full application/login pass.

- Electron 39.8.7; contextIsolation=true, nodeIntegration=false, sandbox=true.
- Bundled current `apps/desktop/src/preload/index.ts` and
  `apps/desktop/src/main/local-review-request.ts` with esbuild.
- Separate temporary userData and localhost fake daemon; no user profile, tokens,
  real task repository, Git mutation, or production service was used.
- Real renderer call returned draft, approval returned approved, foreign runtime
  returned null for caller relay routing, and malformed action was rejected.
- Main-process count: exactly 2 HTTP POSTs (read and approve).
- Screenshot: `mr-electron-ipc.png`.
- Repro sources: `scripts/qa/local-mr/electron-ipc-check.cjs` and
  `scripts/qa/local-mr/electron-ipc-drive.mjs`.

The installed agent-browser CLI attached to a separate blank browser despite its
connect response. Its observed CDP URL did not match fixture port 9337. That
isolated browser was closed; Playwright then directly attached to the verified
Electron endpoint and drove the real test page successfully.

The fake daemon validates fixture profile/auth headers but is not the Go daemon.
Real Git/daemon behavior is covered by separate Go and cross-process tests. This
fixture does not exercise production sign-in, setupDaemonManager lifecycle,
three actual navigation entrances, or deployed multi-machine networking.

The fixture Electron process and browser connection were closed after capture.
