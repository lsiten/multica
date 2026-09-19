package protocol

import (
	"testing"
)

func ptr[T any](v T) *T { return &v }

func baseInput(kind MirrorInputKind) MirrorInputMessage {
	return MirrorInputMessage{
		Kind: kind, GrantID: "grant-1", GestureID: "gesture-1", Seq: 1,
		NativeEpoch: "native-1", DisplayGeneration: "display-1", GeometryRevision: 3,
	}
}

func TestMirrorInputValidation(t *testing.T) {
	valid := []MirrorInputMessage{
		func() MirrorInputMessage {
			m := baseInput(MirrorInputPointerDown)
			m.Pointer = &MirrorPointerInput{Button: MirrorButtonLeft, X: 1, Y: 2}
			return m
		}(),
		func() MirrorInputMessage {
			m := baseInput(MirrorInputPointerMove)
			m.Pointer = &MirrorPointerInput{X: 10, Y: 20}
			return m
		}(),
		func() MirrorInputMessage {
			m := baseInput(MirrorInputWheel)
			m.Pointer = &MirrorPointerInput{X: 1, Y: 2, DeltaY: -30}
			return m
		}(),
		func() MirrorInputMessage {
			m := baseInput(MirrorInputKeyDown)
			m.Key = &MirrorKeyInput{Key: "a", Modifiers: []string{"meta"}}
			return m
		}(),
		func() MirrorInputMessage {
			m := baseInput(MirrorInputType)
			m.Text = &MirrorTextInput{Text: "你好 world"}
			return m
		}(),
	}
	for i, m := range valid {
		if err := m.Validate(); err != nil {
			t.Fatalf("valid case %d rejected: %v", i, err)
		}
	}
}

func TestMirrorInputValidationRejects(t *testing.T) {
	cases := map[string]MirrorInputMessage{
		"missing binding": func() MirrorInputMessage {
			m := baseInput(MirrorInputPointerDown)
			m.NativeEpoch = ""
			m.Pointer = &MirrorPointerInput{Button: MirrorButtonLeft}
			return m
		}(),
		"pointer without button": func() MirrorInputMessage {
			m := baseInput(MirrorInputPointerDown)
			m.Pointer = &MirrorPointerInput{X: 1, Y: 2}
			return m
		}(),
		"wheel with button": func() MirrorInputMessage {
			m := baseInput(MirrorInputWheel)
			m.Pointer = &MirrorPointerInput{Button: MirrorButtonLeft, X: 1, Y: 2}
			return m
		}(),
		"arbitrary key code": func() MirrorInputMessage {
			m := baseInput(MirrorInputKeyDown)
			m.Key = &MirrorKeyInput{Key: "F13"}
			return m
		}(),
		"bad modifier": func() MirrorInputMessage {
			m := baseInput(MirrorInputKeyUp)
			m.Key = &MirrorKeyInput{Key: "enter", Modifiers: []string{"hyper"}}
			return m
		}(),
		"empty type": func() MirrorInputMessage {
			m := baseInput(MirrorInputType)
			m.Text = &MirrorTextInput{}
			return m
		}(),
		"ambiguous payloads": func() MirrorInputMessage {
			m := baseInput(MirrorInputType)
			m.Text = ptr(MirrorTextInput{Text: "x"})
			m.Key = &MirrorKeyInput{Key: "a"}
			return m
		}(),
		"unknown kind": func() MirrorInputMessage {
			m := baseInput("gamepad:press")
			return m
		}(),
		"negative coords": func() MirrorInputMessage {
			m := baseInput(MirrorInputPointerMove)
			m.Pointer = &MirrorPointerInput{X: -1, Y: 0}
			return m
		}(),
	}
	for name, m := range cases {
		if err := m.Validate(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestParseMirrorInputMessage(t *testing.T) {
	m := baseInput(MirrorInputKeyDown)
	m.Key = &MirrorKeyInput{Key: "escape"}
	raw := []byte(`{"kind":"key:down","grant_id":"grant-1","gesture_id":"g","seq":1,"native_epoch":"n","display_generation":"d","geometry_revision":1,"key":{"key":"escape"}}`)
	parsed, err := ParseMirrorInputMessage(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Key.Key != "escape" {
		t.Fatalf("key = %q", parsed.Key.Key)
	}
	if _, err := ParseMirrorInputMessage(nil); err == nil {
		t.Fatal("empty message accepted")
	}
	if _, err := ParseMirrorInputMessage(make([]byte, MaxMirrorInputBytes+1)); err == nil {
		t.Fatal("oversize message accepted")
	}
}
