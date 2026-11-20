package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeAgentCollaborationCommandRejectsTrailingInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{name: "whitespace is allowed", body: "{\"client_request_id\":\"req-1\",\"content\":\"work\"} \n\t", want: http.StatusOK},
		{name: "second json value", body: `{"client_request_id":"req-1","content":"work"}{}`, want: http.StatusBadRequest},
		{name: "trailing garbage", body: `{"client_request_id":"req-1","content":"work"} trailing`, want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			res := httptest.NewRecorder()
			var got agentCollaborationCommand
			if ok := decodeAgentCollaborationCommand(res, req, &got); tc.want == http.StatusOK && !ok {
				t.Fatal("valid JSON was rejected")
			} else if tc.want != http.StatusOK && ok {
				t.Fatal("trailing input was accepted")
			}
			if res.Code != tc.want {
				t.Fatalf("status = %d, want %d", res.Code, tc.want)
			}
		})
	}
}

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
