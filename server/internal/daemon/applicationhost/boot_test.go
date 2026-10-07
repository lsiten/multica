package applicationhost

import "testing"

func TestApplicationBootIdentityIsStableAndRejectsUnknownRecoveryEvidence(t *testing.T) {
	first, err := CurrentBootID()
	if err != nil {
		t.Skipf("boot identity unavailable: %v", err)
	}
	second, err := CurrentBootID()
	if err != nil || first != second {
		t.Fatalf("boot identity changed without reboot: first=%s second=%s error=%v", first, second, err)
	}
	for _, test := range []struct {
		boot     string
		previous bool
	}{{first, false}, {"", false}, {"00000000-0000-4000-8000-000000000000", true}} {
		previous, err := PreviousBoot(Record{BootID: test.boot})
		if err != nil || previous != test.previous {
			t.Fatalf("previous boot=%v expected=%v error=%v", previous, test.previous, err)
		}
	}
	record := hostTestRecord(t)
	if record.BootID != first {
		t.Fatal("new host did not retain its boot identity")
	}
	record.BootID = "untrusted-boot-value"
	if err := record.Validate(); err == nil {
		t.Fatal("malformed persisted boot identity accepted")
	}
}
