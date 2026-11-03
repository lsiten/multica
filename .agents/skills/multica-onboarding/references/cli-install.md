# Official CLI installation and compatibility

Use this branch when `multica` is absent, the installed binary cannot run, or
the user explicitly asks to evaluate an upgrade. Preserve a healthy existing
binary until the candidate has passed the relevant deployment checks.

## Network model

- The CLI is downloaded from a public release source: the upstream GitHub
  Releases, or a deployment-provided distribution.
- The deployment endpoint is whatever host the deployment runs on; it may be
  public or private.
- Do not ask the user to connect a VPN by default.
- If the public release API succeeds but the deployment endpoint fails, first
  suspect an external-internet VPN/proxy or a route override. Retry the
  deployment endpoint with the HTTP proxy bypassed, then ask the user to
  disconnect the external VPN or exclude the deployment hostname/private route
  from it.

Probe without printing proxy configuration:

```bash
curl -fsS -o /dev/null --connect-timeout 5 --max-time 15 \
  https://api.github.com/repos/multica-ai/multica/releases/latest
curl --noproxy '*' -fsS -o /dev/null --connect-timeout 5 --max-time 15 \
  "<SERVER_URL>/health"
```

`--noproxy '*'` bypasses HTTP(S) proxy settings. It cannot repair an OS-level
VPN route; the user may need to disable that external VPN or adjust split
routing. If both public and deployment probes fail, report a broader network or
DNS problem rather than guessing that Multica is down.

## Authoritative public sources

Install the CLI from the command line only:

- Release API: `https://api.github.com/repos/multica-ai/multica/releases/latest`
  (or the deployment-provided distribution the deployment owner names).
- Release assets: `https://github.com/multica-ai/multica/releases/download/<tag>/<asset>`
  (or the deployment-provided asset URLs).

Do not send the user to the multica.ai download page unless the user's
deployment is Multica Cloud: it leads to the desktop app, which may only offer
a sign-in method the deployment does not use. Do not use
`https://get.multica.ai`; it is not the supported download contract.
Do not pipe the moving `main/scripts/install.sh` directly into a shell for this
self-hosted workflow. It selects the latest release and removes the opportunity
to verify the candidate before installation.

## Download an exact verified release

1. Detect the platform with `uname -s` and `uname -m` on macOS/Linux, or use
   the native Windows architecture information.
2. Read the latest release metadata and record the exact tag before downloading.
   Do not use a mutable URL after this point.
3. Select the matching CLI asset:
   - macOS/Linux: `multica-cli-<version>-<os>-<arch>.tar.gz`
   - Windows: `multica-cli-<version>-windows-<arch>.zip`
4. Download both that asset and `checksums.txt` from the same release.
5. Compute SHA-256 locally and require an exact match with the asset's entry in
   `checksums.txt`. Stop on a missing entry or mismatch.
6. Extract to a newly created temporary directory. Run that temporary binary's
   `--version` and `setup self-host --help` before changing `PATH`.
7. Prefer a user-writable binary directory. If administrator elevation is
   required, explain the exact destination and ask the user before installing.

Never delete or overwrite a working binary before the candidate has been
verified. If replacing one, retain a rollback copy until onboarding completes.

## Deployment compatibility probe

The deployment may expose non-secret build metadata at a config endpoint:

```bash
curl --noproxy '*' -fsSL "<SERVER_URL>/config"
```

Record `server_version` when it is present, but do not infer full compatibility
from version text alone: the deployment Server can be a custom build rather than
an official semver release. A build without a version stamp omits the field
entirely; treat a missing `server_version` as "unknown", not as an error.

After the user completes login, verify the candidate with read-only operations:

```bash
multica auth status
multica workspace list --output json
multica runtime list --output json
multica agent list --output json
multica skill list --output json
```

These checks establish authentication and core API compatibility. Full local
Runtime compatibility additionally requires `setup self-host` to complete, the
daemon to become healthy, the intended Runtime to appear online, and no existing
task to be disrupted. Do not create an Issue, mutate an Agent, or execute a
paid model merely as a compatibility probe.

For a fresh install, continue with the normal self-host setup after checksum and
temporary-binary verification. For an upgrade, do not replace a healthy binary
until the user approves the candidate and the rollback path is clear.

## Compatibility result language

Use one of these precise outcomes:

- **Download verified:** release asset matches the published checksum;
  deployment compatibility has not yet been tested.
- **Core API compatible:** authentication and the read-only probes succeed;
  daemon/task execution has not yet been tested.
- **Runtime compatible:** setup completed, daemon is healthy, and the intended
  Runtime is online without disrupting active work.
- **Blocked:** state the failing phase, keep or restore the prior binary, and
  identify the exact owner/action needed next.
