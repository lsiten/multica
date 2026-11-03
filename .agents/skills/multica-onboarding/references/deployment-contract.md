# Deployment contract

This contract describes how the CLI and daemon connect to a Multica deployment.
It holds for any self-hosted or private deployment; it does not assume a fixed
host, identity provider, or network.

## Deployment endpoint

A deployment exposes at least a Web/App base URL and an API/Server base URL.
Both are part of the contract; do not swap the bare host, the Web URL, and the
API URL for each other.

| 用途 | 占位 | 说明 |
|---|---|---|
| Web / App | `<APP_URL>` | 用户在浏览器打开的地址 |
| API / Server | `<SERVER_URL>` | CLI 与 daemon 连接的后端地址 |
| Guide | `<GUIDE_URL>` | 通常 `<APP_URL>/guide` |

Resolve these from the user, from an existing `multica config show`, or from a
pasted deployment URL. A single pasted URL under the deployment's base usually
implies the API base on the same host with a different path, but confirm it
with `multica config show` or the deployment's guide rather than guessing.

Configuration precedence is: command-line flag > `MULTICA_*` environment
variable > the selected profile's `config.json` > built-in defaults. When the
configuration looks correct but behavior differs, first check for an overriding
environment variable; never print credential values.

## Recommended setup entry

```bash
multica setup self-host \
  --server-url <SERVER_URL> \
  --app-url <APP_URL>
```

Compared with `multica config set` per field, `setup self-host` probes the
Server first, then saves the configuration, logs in, and starts or restarts the
daemon. With an existing configuration it first asks
`This will reset your configuration. Continue? [y/N]`; after confirmation it
keeps only the new server/app URLs and drops the saved login and default
workspace. When the Server is unreachable it does not overwrite the existing
configuration, but **still exits with code 0**, so re-check with `multica
config show` and `multica auth status` after every run. When a task is running
it refuses to restart the daemon and reports an error, but the configuration and
login are already saved; after the task finishes run `multica daemon restart`,
do not re-run setup.

## Network contract

The CLI is usually downloaded from a public release source (the upstream GitHub
Releases or a deployment-provided distribution); the deployment endpoint may be
public or private.

Routing rule: keep the proxy for public traffic (the CLI download and
OpenAI/Codex); route the deployment endpoint correctly. If the public probe
succeeds but the deployment endpoint fails, first suspect a proxy/VPN route.
Retry the endpoint with the proxy bypassed; if that is the cause, keep
`HTTPS_PROXY`/`HTTP_PROXY` (the daemon's Codex needs them to reach OpenAI) and
add the deployment host to `NO_PROXY` so the deployment is reached directly,
rather than turning the proxy off entirely. Do not ask the user to connect a
VPN by default, and do not print proxy variable values. An OS-level VPN can
still override private routes, in which case `NO_PROXY` alone is not enough. Do
not use the Multica desktop app unless it supports the deployment's sign-in
method: it may only offer a provider the deployment cannot use.

## CLI version contract

A deployment Server and the CLI evolve together. When the CLI is missing,
obtain the exact version from the deployment-provided distribution or the
official download source, verify `checksums.txt`, run it in a temporary
directory first, and then run the deployment compatibility check. Do not run a
remote `curl | bash` that follows a moving `latest`. If the machine already has
a healthy `multica`, do not upgrade just to chase a newer release.

The full release contract should ultimately come from an internal versioned
manifest:

- Server release ID and the compatible CLI version range;
- an immutable asset URL for each OS/Arch;
- SHA-256 and signature;
- Skill version;
- rollback version and deprecation date.

**The current onboarding Skill package does not include a CLI manifest or
cross-platform CLI assets.** This Skill discovers and verifies a candidate
version from the official release, then verifies its actual compatibility
surface with the deployment Server. See `cli-install.md`.

The deployment's config endpoint (commonly `<SERVER_URL>/config` or
`<APP_URL>/api/config`) usually returns `server_version` (a build without a
version stamp omits the field; that is not an error), but a self-hosted
deployment may be a custom commit rather than an official semver. Similar
version text is not full compatibility. Passing authentication, Workspace,
Runtime, Agent, and Skill read-only checks can only report "core API
compatible"; only after setup completes, the daemon is healthy, and the target
Runtime is online can you report "Runtime compatible". Writing an Issue or
running a paid model must not be a default compatibility test.

When an internal compatibility manifest becomes available, prefer its declared
recommended/minimum/maximum CLI versions. Without a manifest, installation must
follow: download the exact version -> verify checksum/signature -> run
temporarily -> keep a rollback version -> re-run read-only verification ->
verify the daemon/Runtime. Do not treat a successful download as compatibility
or a completed install.

## Credential boundary

- Do not read or print the token in `config.json`.
- Do not paste a PAT, cookie, or Authorization header into a command line,
  Issue, chat, log, or screenshot.
- Browser sign-in, device-code authorization, and enterprise identity
  confirmation are completed by the user.
- Diagnostic summaries keep only version, URL, status, and redacted error
  category.
