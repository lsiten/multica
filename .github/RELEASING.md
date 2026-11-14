# Release runbook

## lsiten/multica fork releases

Push a new `vX.Y.Z` tag on the commit to release. `fork-images.yml` publishes
the backend/web images, and `fork-desktop.yml` builds desktop installers from
the same tag. Prerelease tags such as `vX.Y.Z-rc.1` create GitHub prereleases;
dirty tags are rejected. The upstream `release.yml` stays disabled for this fork.

Both fork channels first call `ci.yml` from the caller commit with
`release_acceptance: true`. CI selects every validation scope and requires its
frontend and backend aggregate gates to succeed. The acceptance script also
checks that the checkout matches the caller's `GITHUB_SHA`, then emits
`accepted_sha`. Release builds check out that immutable SHA. Failed, cancelled,
skipped, missing, or path-filtered acceptance blocks the build and publication
through their job dependencies.

The reusable call grants `contents: read` and `pull-requests: read`. Its workflow
source comes from the same commit and its token permissions are bounded by the
caller. [GitHub reusable workflow semantics](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows), [GitHub token permission semantics](https://docs.github.com/en/actions/reference/workflows-and-actions/reusing-workflow-configurations).

The existing CI frontend test job already includes the desktop package. Run the
release contract fixtures locally with `node --test scripts/fork-release-gate.test.mjs scripts/ci-scope.test.mjs scripts/fork-desktop.test.mjs`.
These exercise the real acceptance CLI and publication shell steps with inert
fixtures. The GitHub-hosted CI run and signed installation/update checks remain
part of release-environment acceptance.

The desktop matrix builds both x64 and arm64 on macOS, Windows, and Linux:

- macOS: DMG and ZIP.
- Windows: NSIS EXE.
- Linux: AppImage, DEB, and RPM.

Every build includes the matching Go CLI. Installers, blockmaps, and update
metadata are retained as workflow artifacts for seven days. Only after every
platform passes its checks and builds successfully does the publish job upload
all assets to a draft GitHub Release, then make it public. A failed upload leaves
a draft that can be retried; already public releases are never overwritten.

Desktop update checks and the notification's changelog link target
`lsiten/multica`. Install a build containing this configuration once to switch
an existing installation from the upstream update source.

Signing is optional for producing packages. Configure these repository Actions
secrets for signed releases:

- macOS: `MAC_CSC_LINK`, `MAC_CSC_KEY_PASSWORD` (Developer ID certificate and
  password), plus `APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, and `APPLE_TEAM_ID`
  for notarization.
- Windows: `WIN_CSC_LINK`, `WIN_CSC_KEY_PASSWORD` (code-signing certificate and
  password).

Without those credentials, packages lack a trusted publisher signature and
macOS notarization. macOS automatic installation requires a properly signed app;
publishing update metadata alone does not satisfy the OS signature checks.

## Normal release

Release from a reviewed commit on `main` by creating and pushing a new semantic
version tag such as `v0.18.4`. The Release workflow intentionally has no manual
trigger: a tag push is the only event that can publish binaries, Homebrew
formulae, and container images.

The verification job runs the Go tests and `govulncheck` before any publishing
job starts. The vulnerability scan is fail-closed by default.

CI and release jobs require Go `~1.27.4`: at least 1.27.4, while accepting
newer 1.27 patches. This security floor is independent of the development
minimum in `server/go.mod`. A plain `1.27.x` can resolve to an older patch
while the `setup-go` version manifest catches up with an official Go release.
When raising the security floor, update all Go workflows and the pinned
Go builder image in `Dockerfile` together so shipped binaries also receive
the fixes.

## Emergency vulnerability-scan bypass

Use the bypass only when `govulncheck` itself or its live vulnerability database
is unavailable, or when maintainers have documented a confirmed false positive
that blocks an urgent release. Never use it to publish a release with an
unresolved reachable vulnerability.

1. Record the reason and maintainer approval in the release issue or pull
   request, and confirm no other release is in progress.
2. In **Settings → Secrets and variables → Actions → Variables**, set the
   repository variable `ALLOW_VULN_BYPASS_FOR_TAG` to the exact release tag,
   for example `v0.18.4`.
3. Re-run the failed Release workflow for that tag. A different tag, an empty
   value, or any typo keeps the scan enabled.
4. Confirm the verification log contains the explicit bypass warning and retain
   the workflow URL in the incident record.
5. Delete `ALLOW_VULN_BYPASS_FOR_TAG` immediately after the release run
   completes. The tag-scoped value prevents a concurrent release with another
   tag from inheriting the bypass.

Every Go binary retains its compiler version in the standard Go build metadata;
use `go version -m <binary>` when auditing a downloaded release artifact.
