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
