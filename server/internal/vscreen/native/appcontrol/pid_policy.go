package appcontrol

import (
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// PIDInputDecision binds a native-host policy result to its keyboard context.
// It is never accepted from an Agent, renderer, profile or private RPC request.
type PIDInputDecision struct {
	Certified     bool
	InputSourceID string
}

// PIDInputPolicy matches reviewed, code-owned evidence to native identities and input families.
type PIDInputPolicy struct {
	records     []pidInputRecord
	inputSource func() string
}

type pidInputRecord struct {
	BundleID, AppVersion, AppBuild, SigningID, CodeHash, OSBuild string
	Kind                                                         protocol.VscreenActionKind
	Variant, InputSourceID, EvidenceSHA256                       string
}

// ProductionPIDInputPolicy is the sole production policy source. Add a record only
// with reviewed real App/OS/input-family acceptance evidence; compilation is not evidence.
func ProductionPIDInputPolicy() PIDInputPolicy { return newPIDInputPolicy(nil, currentPIDInputSource) }

func newPIDInputPolicy(records []pidInputRecord, inputSource func() string) PIDInputPolicy {
	return PIDInputPolicy{records: append([]pidInputRecord(nil), records...), inputSource: inputSource}
}
func hexDigest(s string, size int) bool {
	raw, err := hex.DecodeString(s)
	return err == nil && len(raw) == size && hex.EncodeToString(raw) == s
}
func (r pidInputRecord) matches(p Process) bool {
	if p.PID <= 0 || p.Start == "" || !filepath.IsAbs(p.ExecutablePath) || r.BundleID == "" || r.AppVersion == "" || r.AppBuild == "" || r.SigningID == "" || r.OSBuild == "" || r.Variant == "" || !(hexDigest(r.CodeHash, 20) || hexDigest(r.CodeHash, 32)) || !hexDigest(r.EvidenceSHA256, 32) {
		return false
	}
	switch r.Kind {
	case protocol.VscreenActionClick:
		if r.Variant != "coordinate_primary_click" || r.InputSourceID != "" {
			return false
		}
	case protocol.VscreenActionScroll:
		if r.Variant != "pixel_scroll" || r.InputSourceID != "" {
			return false
		}
	case protocol.VscreenActionDrag:
		if r.Variant != "primary_drag" || r.InputSourceID != "" {
			return false
		}
	case protocol.VscreenActionType:
		if r.Variant != "unicode" || r.InputSourceID == "" {
			return false
		}
	case protocol.VscreenActionKey:
		if !strings.HasPrefix(r.Variant, "key:") || r.InputSourceID == "" {
			return false
		}
	default:
		return false
	}
	return p.BundleID == r.BundleID && p.AppVersion == r.AppVersion && p.AppBuild == r.AppBuild && p.SigningID == r.SigningID && p.CodeHash == r.CodeHash && p.OSBuild == r.OSBuild
}

// Decide authorizes an input mechanism, never a replay of recorded business text or
// coordinates. Existing action validation, snapshot, window and native guards remain mandatory.
func (p PIDInputPolicy) Decide(process Process, action protocol.VscreenAction) PIDInputDecision {
	variant := pidActionVariant(action)
	if variant == "" {
		return PIDInputDecision{}
	}
	keyboard := action.Kind == protocol.VscreenActionKey || action.Kind == protocol.VscreenActionType
	source := ""
	if keyboard && p.inputSource != nil {
		source = p.inputSource()
	}
	for _, r := range p.records {
		if r.matches(process) && r.Kind == action.Kind && r.Variant == variant && (!keyboard || source != "" && source == r.InputSourceID) {
			return PIDInputDecision{Certified: true, InputSourceID: source}
		}
	}
	return PIDInputDecision{}
}

// Verification reports reviewed variants for this App identity, not a blanket action grant.
func (p PIDInputPolicy) Verification(process Process) string {
	for _, r := range p.records {
		if r.matches(process) {
			return "verified_variants"
		}
	}
	return "none"
}
func pidActionVariant(action protocol.VscreenAction) string {
	if action.Validate() != nil {
		return ""
	}
	switch action.Kind {
	case protocol.VscreenActionClick:
		if action.Click.Position != nil {
			return "coordinate_primary_click"
		}
		// Semantic element clicks use AXPress; native PID clicks require frame coordinates.
		return ""
	case protocol.VscreenActionType:
		return "unicode"
	case protocol.VscreenActionScroll:
		return "pixel_scroll"
	case protocol.VscreenActionDrag:
		return "primary_drag"
	case protocol.VscreenActionKey:
		modifiers := append([]string(nil), action.Key.Modifiers...)
		sort.Strings(modifiers)
		return "key:" + action.Key.Key + ":" + strings.Join(modifiers, ",")
	}
	return ""
}
