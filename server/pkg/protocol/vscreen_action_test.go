package protocol

import (
	"encoding/json"
	"testing"
)

func actionFixture() VscreenActionRequest {
	return VscreenActionRequest{Target: VscreenActionTarget{Resource: ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "rt", UID: 501}, TaskID: "task", TransactionID: "transaction", LeaseEpoch: 1, Epoch: VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "display", GeometryRevision: 1}, WindowHandle: "opaque-window", SnapshotRevision: 1}, ActionID: "action", Sequence: 1, Action: VscreenAction{Kind: VscreenActionType, Type: &VscreenTypeAction{ElementHandle: "element", Text: "你好e\u0301"}}}
}

func TestVscreenActionParserFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*VscreenActionRequest)
	}{
		{"missing task", func(p *VscreenActionRequest) { p.Target.TaskID = "" }},
		{"missing transaction", func(p *VscreenActionRequest) { p.Target.TransactionID = "" }},
		{"foreign runtime", func(p *VscreenActionRequest) { p.Target.Resource.RuntimeID = "other" }},
		{"foreign task", func(p *VscreenActionRequest) { p.Target.TaskID = "other" }},
		{"stale lease", func(p *VscreenActionRequest) { p.Target.LeaseEpoch++ }},
		{"stale native", func(p *VscreenActionRequest) { p.Target.Epoch.NativeEpoch = "old" }},
		{"stale display", func(p *VscreenActionRequest) { p.Target.Epoch.DisplayGeneration = "old" }},
		{"stale geometry", func(p *VscreenActionRequest) { p.Target.Epoch.GeometryRevision++ }},
		{"stale snapshot", func(p *VscreenActionRequest) { p.Target.SnapshotRevision++ }},
		{"foreign window", func(p *VscreenActionRequest) { p.Target.WindowHandle = "other" }},
		{"missing action", func(p *VscreenActionRequest) { p.ActionID = "" }},
		{"missing sequence", func(p *VscreenActionRequest) { p.Sequence = 0 }},
		{"unknown action", func(p *VscreenActionRequest) { p.Action.Kind = "global-input" }},
		{"multiple action variants", func(p *VscreenActionRequest) { p.Action.Click = &VscreenClickAction{ElementHandle: "other"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := actionFixture()
			expected := p.Target
			tc.change(&p)
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseVscreenAction(raw, expected); err == nil {
				t.Fatalf("accepted %s", tc.name)
			}
		})
	}
}

func TestVscreenActionParserUnicodeAndPhysicalInput(t *testing.T) {
	p := actionFixture()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseVscreenAction(raw, p.Target)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Action.Type.Text != p.Action.Type.Text {
		t.Fatal("unicode input changed")
	}
	for _, suffix := range []string{`,"source":{"kind":"physical","source_id":"main"}}`, `,"pid":123}`, `,"unknown":true}`} {
		forged := append(append([]byte{}, raw[:len(raw)-1]...), []byte(suffix)...)
		if _, err := ParseVscreenAction(forged, p.Target); err == nil {
			t.Fatalf("accepted arbitrary target: %s", forged)
		}
	}
}

func TestVscreenReceiptRejectsDefaultAndUncertainSuccess(t *testing.T) {
	r := VscreenActionReceipt{ActionID: "action", Sequence: 1, Target: actionFixture().Target, State: VscreenReceiptSucceeded, Outcome: VscreenActionDispatched}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Outcome = VscreenActionUncertain
	if err := r.Validate(); err == nil {
		t.Fatal("uncertain native outcome was accepted as success")
	}
	if err := (VscreenActionReceipt{}).Validate(); err == nil {
		t.Fatal("default receipt accepted")
	}
}

func TestVscreenDragRequiresBoundedObservedPath(t *testing.T) {
	p := actionFixture()
	p.Action = VscreenAction{Kind: VscreenActionDrag, Drag: &VscreenDragAction{From: VscreenPoint{X: 1, Y: 2}, To: VscreenPoint{X: 100, Y: 200}, DurationMS: 400}}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVscreenAction(raw, p.Target); err != nil {
		t.Fatal(err)
	}
	p.Action.Drag.DurationMS = 0
	if err := p.Validate(); err == nil {
		t.Fatal("zero duration drag accepted")
	}
	p.Action.Drag.DurationMS = 3001
	if err := p.Validate(); err == nil {
		t.Fatal("drag exceeding native deadline accepted")
	}
	p.Action.Drag.DurationMS = 400
	p.Action.Drag.To.X = -1
	if err := p.Validate(); err == nil {
		t.Fatal("negative drag frame coordinate accepted")
	}
}

func TestVscreenActionRejectsMissingInputFields(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"type","type":{"element_handle":"element"}}`,
		`{"kind":"click","click":{"position":{}}}`,
		`{"kind":"drag","drag":{"to":{"x":10,"y":20},"duration_ms":500}}`,
		`{"kind":"scroll","scroll":{"delta_x":10,"delta_y":20}}`,
	} {
		var action VscreenAction
		if err := json.Unmarshal([]byte(raw), &action); err == nil {
			if err := action.Validate(); err == nil {
				t.Fatalf("accepted missing input fields: %s", raw)
			}
		}
	}
	var action VscreenAction
	if err := json.Unmarshal([]byte(`{"kind":"type","type":{"element_handle":"element","text":""}}`), &action); err != nil {
		t.Fatal(err)
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("explicit clear value rejected: %v", err)
	}
}
