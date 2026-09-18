package appcontrol

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func policyFixture(t *testing.T) (PIDInputPolicy, Process, protocol.VscreenAction) {
	t.Helper()
	p := Process{PID: 123, UID: 501, Start: "1:2", BundleID: "test.synthetic", OSBuild: "synthetic-os", ExecutablePath: "/synthetic/App.app/Contents/MacOS/App", SigningID: "test.synthetic", CodeHash: strings.Repeat("a", 40), AppVersion: "1.0", AppBuild: "7"}
	action := protocol.VscreenAction{Kind: protocol.VscreenActionKey, Key: &protocol.VscreenKeyAction{Key: "ArrowLeft"}}
	row := pidInputRecord{BundleID: p.BundleID, OSBuild: p.OSBuild, SigningID: p.SigningID, CodeHash: p.CodeHash, AppVersion: p.AppVersion, AppBuild: p.AppBuild, InputSourceID: "synthetic-layout", Kind: action.Kind, Variant: pidActionVariant(action), EvidenceSHA256: strings.Repeat("b", 64)}
	return newPIDInputPolicy([]pidInputRecord{row}, func() string { return "synthetic-layout" }), p, action
}

func TestPIDPolicyProductionHasNoVerifiedRows(t *testing.T) {
	_, p, action := policyFixture(t)
	policy := ProductionPIDInputPolicy()
	if policy.Decide(p, action).Certified || policy.Verification(p) != "none" {
		t.Fatal("production invented certification")
	}
}
func TestPIDPolicyExactIdentityActionAndEvidence(t *testing.T) {
	for _, tc := range []string{"exact", "bundle", "os", "code", "signing", "version", "build", "layout", "missing", "action", "variant", "modifiers", "invalid"} {
		t.Run(tc, func(t *testing.T) {
			policy, p, action := policyFixture(t)
			switch tc {
			case "bundle":
				p.BundleID = "other"
			case "os":
				p.OSBuild = "other"
			case "code":
				p.CodeHash = strings.Repeat("c", 40)
			case "signing":
				p.SigningID = "other"
			case "version":
				p.AppVersion = "2"
			case "build":
				p.AppBuild = "8"
			case "layout":
				policy.inputSource = func() string { return "other" }
			case "missing":
				p.ExecutablePath = ""
			case "action":
				action = protocol.VscreenAction{Kind: protocol.VscreenActionScroll, Scroll: &protocol.VscreenScrollAction{}}
			case "variant":
				action.Key.Key = "ArrowRight"
			case "modifiers":
				action.Key.Modifiers = []string{"shift"}
			case "invalid":
				action.Kind = "unknown"
			}
			if got := policy.Decide(p, action).Certified; got != (tc == "exact") {
				t.Fatalf("case %s allowed=%t", tc, got)
			}
		})
	}
}

func TestPIDPolicyDynamicParametersRemainUsableWithinValidatedFamily(t *testing.T) {
	_, p, _ := policyFixture(t)
	tests := []struct{ first, second protocol.VscreenAction }{
		{protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: "one", Text: "hello"}}, protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: "new-snapshot", Text: "中文 é 任意正文"}}},
		{protocol.VscreenAction{Kind: protocol.VscreenActionClick, Click: &protocol.VscreenClickAction{Position: &protocol.VscreenPoint{X: 1, Y: 2}}}, protocol.VscreenAction{Kind: protocol.VscreenActionClick, Click: &protocol.VscreenClickAction{Position: &protocol.VscreenPoint{X: 400, Y: 300}}}},
		{protocol.VscreenAction{Kind: protocol.VscreenActionScroll, Scroll: &protocol.VscreenScrollAction{DeltaY: 1}}, protocol.VscreenAction{Kind: protocol.VscreenActionScroll, Scroll: &protocol.VscreenScrollAction{Position: protocol.VscreenPoint{X: 50, Y: 80}, DeltaY: -120, DeltaX: 5}}},
		{protocol.VscreenAction{Kind: protocol.VscreenActionDrag, Drag: &protocol.VscreenDragAction{DurationMS: 10}}, protocol.VscreenAction{Kind: protocol.VscreenActionDrag, Drag: &protocol.VscreenDragAction{From: protocol.VscreenPoint{X: 20, Y: 30}, To: protocol.VscreenPoint{X: 60, Y: 90}, DurationMS: 700}}},
	}
	for _, tc := range tests {
		t.Run(string(tc.first.Kind), func(t *testing.T) {
			row := pidInputRecord{BundleID: p.BundleID, AppVersion: p.AppVersion, AppBuild: p.AppBuild, CodeHash: p.CodeHash, SigningID: p.SigningID, OSBuild: p.OSBuild, Kind: tc.first.Kind, Variant: pidActionVariant(tc.first), EvidenceSHA256: strings.Repeat("b", 64)}
			if tc.first.Kind == protocol.VscreenActionType {
				row.InputSourceID = "synthetic-layout"
			}
			policy := newPIDInputPolicy([]pidInputRecord{row}, func() string { return "synthetic-layout" })
			if !policy.Decide(p, tc.first).Certified || !policy.Decide(p, tc.second).Certified {
				t.Fatal("policy became a recorded-payload replay filter")
			}
		})
	}
}

func TestPIDPolicyDecisionUsesOneSourceAndImmutableRecords(t *testing.T) {
	policy, p, action := policyFixture(t)
	calls := 0
	policy.inputSource = func() string {
		calls++
		if calls == 1 {
			return "synthetic-layout"
		}
		return "changed"
	}
	decision := policy.Decide(p, action)
	if !decision.Certified || decision.InputSourceID != "synthetic-layout" || calls != 1 {
		t.Fatalf("decision=%+v calls=%d", decision, calls)
	}
	if policy.Decide(p, action).Certified {
		t.Fatal("changed source retained eligibility")
	}
	row := policy.records[0]
	records := []pidInputRecord{row}
	copyPolicy := newPIDInputPolicy(records, func() string { return "synthetic-layout" })
	records[0].EvidenceSHA256 = ""
	if !copyPolicy.Decide(p, action).Certified {
		t.Fatal("caller mutated policy records")
	}
	for _, evidence := range []string{"", strings.Repeat("z", 64)} {
		row.EvidenceSHA256 = evidence
		if newPIDInputPolicy([]pidInputRecord{row}, func() string { return "synthetic-layout" }).Decide(p, action).Certified {
			t.Fatal("missing or malformed evidence accepted")
		}
	}
	policy.inputSource = func() string { return "" }
	if policy.Decide(p, action).Certified {
		t.Fatal("unknown source accepted")
	}
}
