package computeruse

import "testing"

func TestParseActionBoundedCoordinates(t *testing.T) {
	got, err := ParseAction("Thought: button visible\nAction: click(start_box='(100, 200)')", 1920, 1080)
	if err != nil || got.Kind != "click" || got.X != 100 || got.Y != 200 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, output := range []string{
		"Action: click(start_box='(1920, 20)')",
		"Action: click(start_box='(-1, 20)')",
		"Action: click(start_box='(1,2)'); import os",
		"Action: wait()\nAction: finished()",
		"Action: unknown()",
	} {
		if _, err := ParseAction(output, 1920, 1080); err == nil {
			t.Errorf("accepted unsafe output %q", output)
		}
	}
}
