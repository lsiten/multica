package service

import "testing"

func TestHumanRequestActionableContract(t *testing.T) {
	valid := HumanRequestInput{Key: "screen-access", Kind: "manual", Title: "Enable screen recording", Steps: []string{"Open System Settings > Privacy & Security > Screen Recording and enable Multica"}, ActionLabel: "Check permission", Next: "I will check the permission before continuing", Verification: "Check the screen recording permission on the runtime"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*HumanRequestInput)
	}{
		{"missing action", func(r *HumanRequestInput) { r.Title = "" }},
		{"missing steps", func(r *HumanRequestInput) { r.Steps = nil }},
		{"missing verification", func(r *HumanRequestInput) { r.Verification = "" }},
		{"missing continuation", func(r *HumanRequestInput) { r.Next = "" }},
		{"unknown kind", func(r *HumanRequestInput) { r.Kind = "automatic" }},
		{"unknown purpose", func(r *HumanRequestInput) { r.Purpose = "background_control" }},
		{"desktop approval must be a card", func(r *HumanRequestInput) {
			r.Kind = "confirmation"
			r.Steps = nil
			r.Verification = ""
			r.Purpose = "desktop_approval"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := valid
			tc.change(&in)
			if in.Validate() == nil {
				t.Fatal("accepted an unactionable request")
			}
		})
	}
}

func TestHumanRequestResponseMatchesTheRequest(t *testing.T) {
	request := HumanRequestInput{Kind: "choice", Choices: []HumanRequestChoice{{ID: "a", Label: "Use A"}, {ID: "b", Label: "Use B"}}}
	for _, tc := range []struct {
		response HumanRequestAnswer
		valid    bool
	}{
		{HumanRequestAnswer{Revision: 1, Decision: "choice", Answer: "a"}, true},
		{HumanRequestAnswer{Revision: 1, Decision: "choice", Answer: "invented"}, false},
		{HumanRequestAnswer{Revision: 1, Decision: "approve"}, false},
		{HumanRequestAnswer{Revision: 1, Decision: "reject"}, true},
		{HumanRequestAnswer{Revision: 0, Decision: "choice", Answer: "a"}, false},
	} {
		if (tc.response.Validate(request) == nil) != tc.valid {
			t.Errorf("response %+v: valid=%v", tc.response, tc.valid)
		}
	}
}

func TestHumanRequestAffectedIssueIDsAreBoundedAndUnique(t *testing.T) {
	base := HumanRequestInput{
		Key: "requirement-change-review", Kind: "choice", Title: "Requirement changed",
		ActionLabel: "Decide", Next: "Apply the selected impact",
		Choices: []HumanRequestChoice{{ID: "keep", Label: "Keep"}, {ID: "redo", Label: "Redo"}},
	}
	base.AffectedIssueIDs = []string{"child-a", "child-a"}
	if base.Validate() == nil {
		t.Fatal("accepted duplicate affected issue IDs")
	}
	base.AffectedIssueIDs = make([]string, 101)
	for i := range base.AffectedIssueIDs {
		base.AffectedIssueIDs[i] = string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	if base.Validate() == nil {
		t.Fatal("accepted more than 100 affected issue IDs")
	}
}
