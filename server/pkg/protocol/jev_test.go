package protocol

import (
	"strings"
	"testing"
)

func TestLocalJevConfigAcceptsPinnedCustomModels(t *testing.T) {
	config := WorkspaceJevConfig{Source: "local", ModelID: "Example/decider", ModelRevision: strings.Repeat("a", 40), Device: "cpu", TimeoutSeconds: 45}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []string{"main", "../revision", "", strings.Repeat("a", 39)} {
		invalid := config
		invalid.ModelRevision = revision
		if invalid.Validate() == nil {
			t.Fatal("mutable/invalid revision accepted", revision)
		}
	}
	invalid := config
	invalid.ModelID = "../model"
	if invalid.Validate() == nil {
		t.Fatal("unsafe repository accepted")
	}
}
