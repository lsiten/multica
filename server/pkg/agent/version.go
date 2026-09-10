package agent

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MinVersions defines the minimum required CLI version for each agent type.
// Versions below these will be rejected during daemon registration.
var MinVersions = map[string]string{
	"claude":   "2.0.0",
	"codex":    "0.100.0", // app-server --listen stdio:// added in 0.100.0
	"copilot":  "1.0.0",   // --output-format json envelope stable from 1.0.x
	"grok":     "0.2.89",  // ACP + authenticate/session-load/set_model/MCP and --effort thinking flag
	"qwen":     "0.20.0",  // stream-json protocol captured and verified against Qwen Code 0.20.0
	"dim":      "0.3.10",  // cross-run session/load: per-process lock releases on graceful exit
	"mcode":    "0.1.2",   // ACP v1 session/new, prompt, MCP capability forwarding
	"zeroclaw": "0.8.0",   // persistent ACP sessions and session/resume were added in 0.8.0
}

// MinQuickCreateCLIVersion gates the agent-create (quick-create) flow against
// the multica CLI version reported by the daemon at registration time. The
// quick-create prompt that the agent runs depends on CLI behavior introduced
// after this version (attachment URL handling, quick-create attachment
// binding, no-retry semantics on `multica issue create` failure — see PR
// #1851); older daemons would either double-create issues or mishandle pasted
// screenshot URLs. Treated as a hard requirement: missing / unparsable / below
// this threshold all fail closed.
const MinQuickCreateCLIVersion = "0.2.21"

// MinQuickCreateFieldsCLIVersion is the first daemon release that carries
// explicit quick-create priority and due-date fields from the claim response
// into the generated issue-create prompt. Basic quick-create remains on the
// older floor above; only requests using these optional fields need this gate.
const MinQuickCreateFieldsCLIVersion = "0.4.3"

// MinLocalWorktreeCLIVersion is the release that first shipped
// execution_mode=worktree for local_directory resources (MUL-5707).
//
// NOTHING GATES ON THIS. It is a display value: the number shown in the 422
// payload and the UI hint so a user knows roughly which release to update to.
// The gates themselves read protocol.DaemonCapabilityLocalWorktreeV1, which
// the daemon advertises only when it actually implements the mode.
//
// It stopped being a gate because it could not be one. A daemon without the
// implementation does not lose a field — it runs the task IN PLACE, editing the
// working copy the user asked to isolate. Version strings cannot answer that:
// CheckMinCLIVersionFor exempts git-describe dev builds so `make daemon` stays
// unblocked, and a v0.4.23-era daemon reporting "v0.4.21-24-gcd3c0bb89" sailed
// through the floor and ran two tasks in the user's own directory.
const MinLocalWorktreeCLIVersion = "0.4.24"

// Errors returned by CheckMinCLIVersion. Callers branch on these to surface
// "needs upgrade" vs "version not reported" with the right user message.
var (
	ErrCLIVersionMissing = errors.New("multica CLI version not reported by daemon")
	ErrCLIVersionTooOld  = errors.New("multica CLI version is below required minimum")
)

// devBuildRe covers the source-build default `dev` and git-describe output:
// commits past a tag, a dirty tag, or a bare hash when no tag is reachable.
// Keep it in sync with DEV_BUILD_RE in packages/core/runtimes/cli-version.ts
// so every development build passes both gates while tagged releases still
// use the minimum version.
var devBuildRe = regexp.MustCompile(`^(?:dev|[0-9a-fA-F]{4,64}(?:-dirty)?|v?\d+\.\d+\.\d+-(?:\d+-g[0-9a-fA-F]+(?:-dirty)?|dirty))$`)

// CheckMinCLIVersion returns nil when `detected` parses as ≥ minimum. Returns
// ErrCLIVersionMissing for empty or unparsable input, and ErrCLIVersionTooOld
// when parsable but below the minimum. The caller can check for these
// sentinel errors with errors.Is to drive the response shape.
//
// Dev-built daemons (`dev` or git-describe builds) always pass — the version string
// itself is the shared signal, so the modal pre-check and this server gate
// agree by construction without needing to compare separate env flags.
func CheckMinCLIVersion(detected string) error {
	return CheckMinCLIVersionFor(detected, MinQuickCreateCLIVersion)
}

// CheckMinCLIVersionFor applies the quick-create version policy against a
// caller-provided capability floor. It preserves the dev-build exemption so
// feature-specific server and frontend gates agree with the base gate.
func CheckMinCLIVersionFor(detected, minimum string) error {
	d := strings.TrimSpace(detected)
	if d == "" {
		return ErrCLIVersionMissing
	}
	if devBuildRe.MatchString(d) {
		return nil
	}
	parsed, err := parseSemver(d)
	if err != nil {
		return ErrCLIVersionMissing
	}
	min, err := parseSemver(minimum)
	if err != nil {
		// Misconfiguration in the constant itself — fail closed as missing.
		return ErrCLIVersionMissing
	}
	if parsed.lessThan(min) {
		return ErrCLIVersionTooOld
	}
	return nil
}

// semver holds a parsed semantic version (major.minor.patch).
type semver struct {
	Major, Minor, Patch int
}

// versionRe matches version strings like "2.1.100", "v2.0.0", or
// "2.1.100 (Claude Code)" — it extracts the first three numeric components.
var versionRe = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

// parseSemver extracts a semver from a version string.
func parseSemver(raw string) (semver, error) {
	m := versionRe.FindStringSubmatch(raw)
	if m == nil {
		return semver{}, fmt.Errorf("cannot parse version %q", raw)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return semver{Major: major, Minor: minor, Patch: patch}, nil
}

// lessThan returns true if v < other.
func (v semver) lessThan(other semver) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}

// BelowMinimumError reports a version that parsed successfully and is below
// the configured minimum. It is a distinct type so callers can tell a
// CONFIRMED too-old verdict apart from "could not parse the version": only
// the former is evidence strong enough to act on (taking a runtime offline),
// while an unreadable version must be treated like any other failed
// detection and leave working runtimes alone.
type BelowMinimumError struct {
	AgentType string
	Detected  string
	Minimum   string
}

func (e *BelowMinimumError) Error() string {
	return fmt.Sprintf("%s version %s is below minimum required %s — please upgrade", e.AgentType, e.Detected, e.Minimum)
}

// CheckMinVersion validates that detectedVersion meets the minimum for agentType.
// Returns nil if the version is acceptable or no minimum is defined, a
// *BelowMinimumError when the version parsed and is confirmed too old, and a
// plain error when the version could not be parsed at all.
func CheckMinVersion(agentType, detectedVersion string) error {
	minRaw, ok := MinVersions[agentType]
	if !ok {
		return nil
	}
	min, err := parseSemver(minRaw)
	if err != nil {
		return fmt.Errorf("invalid minimum version %q for %s: %w", minRaw, agentType, err)
	}
	detected, err := parseSemver(detectedVersion)
	if err != nil {
		return fmt.Errorf("cannot parse detected %s version %q: %w", agentType, detectedVersion, err)
	}
	if detected.lessThan(min) {
		return &BelowMinimumError{AgentType: agentType, Detected: detectedVersion, Minimum: minRaw}
	}
	return nil
}
