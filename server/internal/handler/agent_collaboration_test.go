package handler

import "testing"

func TestCollaborationFingerprintIsStableAndDelimited(t *testing.T) {
	first := collaborationFingerprint("append", "request-1", "run-1", "a", "bc")
	if first != collaborationFingerprint("append", "request-1", "run-1", "a", "bc") {
		t.Fatal("same collaboration command changed its fingerprint")
	}
	if first == collaborationFingerprint("append", "request-1", "run-1", "ab", "c") {
		t.Fatal("fingerprint dropped argument boundaries")
	}
}

func TestCollaborationTruncateCountsRunes(t *testing.T) {
	if got := collaborationTruncate("  汉字🙂  ", 3); got != "汉字🙂" {
		t.Fatalf("truncate = %q", got)
	}
	if got := collaborationTruncate("abcdef", 3); got != "abc…" {
		t.Fatalf("truncated suffix = %q", got)
	}
}
