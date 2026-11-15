package protocol

import (
	"strings"
	"testing"
)

func TestAgentDesktopControlInputValidation(t *testing.T) {
	source := MirrorSource{Kind: MirrorSourcePhysical, SourceID: "display:1"}
	point := 0.5
	tests := []struct {
		name    string
		input   AgentDesktopControlInput
		wantErr bool
	}{
		{name: "click", input: AgentDesktopControlInput{Source: source, Action: AgentDesktopControlClick, X: &point, Y: &point}},
		{name: "relative move", input: AgentDesktopControlInput{Source: source, Action: AgentDesktopControlMoveRelative, X: &point, Y: &point}},
		{name: "keyboard", input: AgentDesktopControlInput{Source: source, Action: AgentDesktopControlPress, Key: "cmd+c"}},
		{name: "missing click point", input: AgentDesktopControlInput{Source: source, Action: AgentDesktopControlClick}, wantErr: true},
		{name: "oversized text", input: AgentDesktopControlInput{Source: source, Action: AgentDesktopControlText, Text: strings.Repeat("x", 65537)}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.input.Validate()
			if test.wantErr && err == nil {
				t.Fatal("invalid input accepted")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("valid input rejected: %v", err)
			}
		})
	}
}
