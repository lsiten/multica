package handler

import (
	"strings"
	"testing"
)

func TestIdentityEmailActionAllowedRequiresExplicitGrant(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "email granted", raw: `{"multica_autonomy":{"allowed_identity_actions":["email"]}}`, want: true},
		{name: "other action only", raw: `{"multica_autonomy":{"allowed_identity_actions":["phone"]}}`, want: false},
		{name: "missing policy", raw: `{}`, want: false},
		{name: "malformed policy", raw: `{"multica_autonomy":true}`, want: false},
		{name: "malformed config", raw: `{`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := identityEmailActionAllowed([]byte(tt.raw)); got != tt.want {
				t.Fatalf("identityEmailActionAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeIdentityEmailRecipientRejectsDisplayNamesAndHeaders(t *testing.T) {
	if got, domain, ok := normalizeIdentityEmailRecipient("Alice@example.test"); !ok || got != "Alice@example.test" || domain != "example.test" {
		t.Fatalf("normalized recipient = (%q, %q, %v)", got, domain, ok)
	}
	for _, recipient := range []string{"Alice <alice@example.test>", "alice@example.test\r\nBcc: bad@example.test", "not-an-email", ""} {
		if _, _, ok := normalizeIdentityEmailRecipient(recipient); ok {
			t.Fatalf("normalizeIdentityEmailRecipient(%q) accepted unsafe recipient", recipient)
		}
	}
}

func TestValidIdentityEmailTextRejectsHeadersAndOversize(t *testing.T) {
	if !validIdentityEmailText("status", "line one\nline two") {
		t.Fatal("valid plain-text body was rejected")
	}
	for _, tc := range []struct {
		subject string
		body    string
	}{
		{subject: "status\r\nBcc: bad@example.test", body: "body"},
		{subject: "status", body: "body\r\nX-Test: value"},
		{subject: "status", body: "body\x00value"},
		{subject: "status", body: strings.Repeat("x", identityEmailMaxBodyBytes+1)},
	} {
		if validIdentityEmailText(tc.subject, tc.body) {
			t.Fatalf("validIdentityEmailText accepted unsafe input: %#v", tc)
		}
	}
}
