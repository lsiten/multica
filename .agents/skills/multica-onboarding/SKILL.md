---
name: multica-onboarding
description: "Use when a coordinating or coding agent must onboard a user to any self-hosted or private Multica deployment: download or import the self-contained onboarding Skill, obtain and verify the official CLI, diagnose network routing for the deployment endpoint, configure a local Runtime, create and verify a Cloud Machine, connect Codex or Pi, add and manage repositories through Repo Manager, or connect a third-party chat channel. Also use when the user asks in Chinese, e.g. 接入 Multica, Multica 入职, 新同事上手 Multica, 装 multica, 连上运行时, 本机连 Multica. This is a discovery-first, secret-safe workflow that works for any deployment authorized through the browser/Web app and assumes no access to the Multica source repository."
---

# Multica onboarding

Use this workflow for any Multica deployment that authenticates through the Web
app (browser login). It does not assume a fixed host, a fixed identity
provider, or a fixed network. Preserve a working installation, never print
stored tokens, and separate human identity steps from actions an agent can
verify.

The deployment's Web/App URL, API/Server URL, and guide URL are specific to your
deployment. Resolve them once at the start (section 1) from the user, from an
existing `multica config show`, or from the guide link, then carry them through
the rest of the flow. Do not assume a fixed host.

This Skill is distributed as one `.skill` archive. Assume the receiving agent
cannot access the Multica source repository. Every supporting file named below
must exist inside the downloaded archive; if one is missing, stop and report an
invalid package instead of trying to read a repository path.

Read the supporting files only when their branch becomes relevant:

- Download, import, and package integrity: `references/distribution.md`
- Official CLI discovery, download, checksum, and compatibility: `references/cli-install.md`
- Install the runtime-global Multica CLI Skill: `references/global-skill.md`
- Product model: `references/concepts.md`
- Deployment endpoint and compatible-CLI policy: `references/deployment-contract.md`
- Cloud Machine and Repo Manager: `references/cloud-machine.md`
- Third-party chat channel entry: `references/channels.md`
- Failure diagnosis: `references/troubleshooting.md`

## 0. Fixed rules: Web app + the deployment's sign-in

State these to the user before anything else; they are not negotiable options:

- **Use the Web app first.** Open `<APP_URL>` in a browser and sign in. Use the
  Multica desktop app only if it offers a sign-in method your deployment
  supports. The desktop app may only offer a login method your deployment does
  not use, so it can get stuck at sign-in. If that happens, do not link to,
  download, or suggest the desktop app; use the Web app.
- **Sign in the way the deployment offers** (SSO, email-code, Google, or the
  deployment's own provider). Keep the same identity everywhere; mixing sign-in
  methods can create a second, empty account (see `references/troubleshooting.md`).
- **Install the CLI from the command line only** (`references/cli-install.md`).
- **The full human-readable guide** lives at `<GUIDE_URL>` (usually
  `<APP_URL>/guide`).

## First-time path for a new colleague

For someone connecting for the first time, this is the whole route. Steps marked
**[human]** must be done by the user; the agent waits and then verifies.

1. **[human]** Sign in once at the Web app and confirm the team workspace is
   visible (section 3 explains what to do if it is not).
2. **[human]** Install Codex and run `codex login`; the agent then checks it
   (section 2).
3. Install the `multica` CLI from the command line and verify its checksum
   (`references/cli-install.md`).
4. In a terminal with the proxy variables and `NO_PROXY` set (section 1), run
   `multica setup self-host` (section 2). **[human]** Authorize the CLI in the
   browser with the deployment's sign-in, and pick the workspace if there is
   more than one.
5. Confirm the Runtime is online and that an Agent the user can use runs on it
   (section 4).
6. Install the multica-cli Skill so Codex can work with issues from now on
   (section 6).
7. Optional, only if the user agrees: run a first real task (section 7).

Use the sections below for the details and for anything that does not go
straight through.

## 1. Discover before changing anything

Ask which outcome the user wants: install this Skill for a coordinating agent,
repair or configure a local Runtime, create a Cloud Machine, connect a model,
manage repositories, connect a chat channel, or install the global Multica CLI
Skill so future pasted Multica links are handled through the authenticated CLI.

Resolve the deployment endpoint before blaming the deployment:

- `<APP_URL>` — the Web/App base URL the user opens in a browser.
- `<SERVER_URL>` — the API/Server base URL the CLI talks to.
- `<GUIDE_URL>` — the human-readable guide, usually `<APP_URL>/guide`.

Get these from the user, from an existing config (`multica config show`), or
from a pasted deployment URL. A single pasted URL under the deployment's base
tells you both: the API base is usually the same host with a different path
(e.g. `.../multica-api`), but confirm it with `multica config show` or the
deployment's guide rather than guessing.

Probe both the CLI download source and the deployment endpoint; do not treat a
reachable one as proof the other works:

```bash
# public CLI release source (GitHub Releases, or a deployment-provided mirror)
curl -fsS -o /dev/null --connect-timeout 5 --max-time 15 \
  https://api.github.com/repos/multica-ai/multica/releases/latest
# the deployment API endpoint (replace the host with the deployment's)
curl --noproxy '*' -fsS -o /dev/null --connect-timeout 5 --max-time 15 \
  "<SERVER_URL>/health"
```

**Routing rule: keep the proxy for public traffic; route the deployment
endpoint correctly.** Public CLI downloads and OpenAI/Codex traffic go through
the proxy; the deployment endpoint may be public or private. If the public
probe succeeds but the deployment endpoint fails, suspect a proxy/VPN route:
keep `HTTPS_PROXY`/`HTTP_PROXY` (the daemon's Codex needs them to reach OpenAI),
and add the deployment host to `NO_PROXY` so the deployment is reached directly.
Do not turn the proxy off entirely, and do not ask the user to connect a VPN by
default. An OS-level VPN can still override private routes, in which case
`NO_PROXY` alone is not enough.

```bash
export NO_PROXY="localhost,127.0.0.1,<deployment-host>" no_proxy="localhost,127.0.0.1,<deployment-host>"
```

Append to an existing `NO_PROXY` rather than replacing it. The daemon inherits
the environment of the terminal that started it; see "daemon started without
the proxy" in `references/troubleshooting.md`.

Only check whether the proxy variables are set (for example
`[ -n "$HTTPS_PROXY" ] && echo set`); never print their values, because proxy
URLs can contain credentials.

If `multica` exists, collect only non-secret state:

```bash
multica --version
multica config show
multica auth status
multica daemon status --output json
multica workspace list --output json
multica runtime list --output json
```

Do not read or echo the token field from `~/.multica/config.json`. Redact tokens,
cookies, authorization headers, personal-access-token values, and private
callback URLs from all summaries.

## 2. Decide the smallest safe path

Use the observations to choose exactly one path:

- **Already healthy:** endpoints match the deployment contract, authentication is
  valid, the intended workspace is visible, and the Runtime is online. Do not
  reinstall or reset configuration.
- **Configuration drift:** explain before running it that setup resets the
  selected profile: it keeps only the new server/app URLs and drops the saved
  login and default workspace, so the user will log in and pick the workspace
  again. After the user accepts the CLI prompt, run the exact self-host setup
  command below.
- **CLI absent:** follow `references/cli-install.md` to discover the current
  official release from the command line, download the exact OS/architecture
  asset, verify it against the release checksum, test it from a temporary
  directory, and only then install it. Do not execute an unpinned remote
  `curl | bash` pipeline.
- **Existing CLI is healthy:** preserve it. A newer official release is not, by
  itself, a reason to replace a working client.
- **Compatibility uncertain:** keep the old binary available, probe the
  deployment Server with the candidate as described in
  `references/cli-install.md`, and report the unverified surface instead of
  claiming full compatibility.
- **Cloud execution wanted:** keep local installation work separate and continue
  with `references/cloud-machine.md`.
- **Chat channel wanted:** first confirm the Multica workspace and entry Agent,
  then continue with `references/channels.md`.

Before the first `setup self-host` or any daemon start/restart, check that the
agent CLI the daemon will launch is ready. The daemon only registers a Codex
Runtime when it can find `codex`, and tasks fail if Codex is not logged in:

```bash
command -v codex && codex --version
codex login status
```

`codex login status` belongs to the Codex CLI, not Multica. If the user's Codex
version does not have it, ask the user to run `codex login` and confirm the
result themselves.

- `codex` not found: ask the user to install Codex first, then re-check. If it
  is installed through nvm/Homebrew but only on an interactive shell's `PATH`,
  see "Codex not found by the daemon" in `references/troubleshooting.md`.
- Not logged in: the user runs `codex login` themselves (a human identity step;
  do not handle their credentials).
- Start the daemon from a terminal that has the proxy variables and `NO_PROXY`
  from step 1 set.

For a local CLI whose version is already approved, configure both URLs together:

```bash
multica setup self-host \
  --server-url <SERVER_URL> \
  --app-url <APP_URL>
```

What `setup self-host` actually does, and how to check the result:

- With an existing configuration it first prints the current values and asks
  `This will reset your configuration. Continue? [y/N]`. Answering no aborts
  with exit code 0 and changes nothing.
- It then probes the Server. **If the Server is unreachable it prints a warning,
  leaves the existing configuration unchanged, and still exits with code 0.**
  Never judge success by the exit code; re-check with `multica config show` and
  `multica auth status` after every run.
- When the probe succeeds it saves only the new URLs (the old token and default
  workspace are gone), opens the browser login, and then starts or restarts the
  daemon.
- If the daemon has active tasks, setup refuses to restart it and exits with an
  error, but the new configuration and login are already saved. Do not re-run
  setup; after those tasks finish, run `multica daemon restart` from a terminal
  with the proxy variables and `NO_PROXY` set.

## 3. Complete human identity steps honestly

Let the user complete the browser login the deployment offers. On SSH or a
remote computer, follow the login command's tunnel hint; use `--callback-host`
only when the browser can reach that host directly. Never claim login succeeded
until `multica auth status` confirms it.

Do not ask the user to create or paste a PAT during the normal local flow.
Browser login first receives a short-lived login credential, then the CLI
creates its own 90-day PAT, verifies it, and stores it in the selected profile's
config with file mode `0600`. The global Multica CLI Skill uses this saved CLI
session; the Skill itself must never contain credentials.

On SSH, prefer the login command's callback tunnel. Only when browser callback
and tunneling are not possible may the user create a PAT in the Web UI and enter
it directly into their own terminal with `multica login --token`. The agent must
not request, receive, echo, or persist that value.

If the workspace list is empty after login, or login lands on a "create
workspace" page, do not create a workspace first. A sign-in through the
deployment's identity provider can auto-join the team workspace; a different
sign-in method (for example an email-code account) can be a separate, empty
account. Confirm the login method, and if the workspace is still empty, ask an
administrator to check default-workspace configuration. Unaccepted invites are a
secondary cause: an invite must be accepted in the Web app before it appears.

## 4. Verify the Runtime and the Agent

The Runtime is the machine that can pick up Agent work. Verify both that it is
online and that an Agent the user can use runs on it.

```bash
multica daemon status --output json
multica runtime list --output json
multica agent list --output json
```

- A healthy daemon and an online Runtime are necessary but not sufficient.
- No diagnostic output contains credentials.

Do not create an issue, assign an Agent, send a channel message, or run a paid
model merely as a connectivity test unless the user explicitly asks.

**Keep the daemon running.** The daemon has no auto-start and is not installed
as a system service. After a reboot or logout the Runtime goes offline until the
user runs `multica daemon start` again, in a terminal with the proxy variables
and `NO_PROXY` set. `multica daemon restart` does not check for running tasks
(only setup does), so before any restart check `multica daemon status --output
json` and the task state in the Web app.

**Confirm there is an Agent to hand work to.** An online Runtime is not enough;
the user assigns work to an Agent that runs on it:

```bash
multica agent list --output json
```

Look for an Agent bound to the user's Runtime. Do not assume one was created
automatically: users who sign in through the deployment's identity provider may
skip the upstream onboarding wizard. If none fits, ask the user whether to create
one bound to their Runtime (`multica agent create --name <name> --runtime-id
<runtime-id>`; confirm the flags with `--help`) or to install a digital employee
from the `<GUIDE_URL>` page. Creating an Agent is a write and needs the user's
go-ahead.

## 5. Teach the next useful path

Explain the minimum product model from `references/concepts.md`, then follow the
user's chosen path:

- Local execution: show how Agents, Issues, and the local Runtime fit together.
- Cloud execution and repositories: use `references/cloud-machine.md`; Cloud
  Agent means more than a remote model, and repository writes must remain under
  Repo Manager control.
- Chat channel entry: use `references/channels.md`; group chat and direct-message
  behavior differ.
- Failure: classify it with `references/troubleshooting.md` before changing
  state.

## 6. Install the runtime-global Multica CLI Skill (recommended default)

After CLI authentication and workspace discovery succeed, install the bundled
`assets/multica-cli/` Skill into the user's external agent harness by default
(still asking before replacing an existing one). This is the recommended way to
work from then on: instead of clicking through the Web UI, the user asks Codex
to create issues, update status, assign, and comment, and Codex does it through
the authenticated `multica` CLI. It also lets a later Codex, Claude Code,
Cursor, or similar session recognize a pasted Multica URL and read or act on it.

Follow `references/global-skill.md`. Treat this as a filesystem write: inspect
the target, ask before replacing an existing Skill, copy the whole bundled
directory, and tell the user to restart or reload the harness. Do not import it
as a Multica Workspace Skill unless the user separately wants Multica-managed
Agents to receive it.

Validate the installation with a read of the user's example link or a link under
the deployment's base (the check itself is read-only; writes happen later, when
the user asks):

```text
<APP_URL>/<workspace-slug>/issues
```

The path contains a Workspace slug, not a Workspace UUID. Resolve it with
`multica workspace get <slug> --output json` before passing the returned UUID to
`--workspace-id`. Never write an Issue merely to validate Skill installation.

## 7. Optional: run a first real task

Only if the user explicitly agrees. This is the best demonstration of working
through the CLI instead of the Web UI:

1. Ask the user for a small, real piece of work and the Agent to give it to.
2. Create the issue with the CLI and assign it to that Agent (`multica issue
   create --title ... --assignee ...`; confirm flags with `--help`).
3. Watch the task start on the user's own Runtime (`multica issue runs <issue>`
   or the Web app) and show where the result comes back on the issue.

Without that consent, keep the rule from section 4: no issues or model runs as a
mere connectivity test.

## 8. Return a redacted completion report

Report:

- CLI version and whether it was already approved;
- official CLI release source, checksum result, and compatibility surface when
  this run installed or evaluated the CLI;
- Skill package source and integrity result when this run installed the Skill;
- deployment endpoint reachability and whether a proxy/VPN route had to be
  bypassed;
- endpoint match/mismatch;
- authentication state, workspace selection, and Runtime state;
- the Agent available on that Runtime, and whether one was created;
- the first real task and its result, if the user chose to run one;
- local or Cloud execution path;
- runtime-global Multica CLI Skill target and reload state;
- whether a pasted deployment URL was parsed and read successfully through the
  CLI;
- chat channel connection state if requested;
- unresolved blocker and the exact human owner/action.

Never include token values, cookies, authorization headers, internal signing
material, or full credential-bearing URLs.
