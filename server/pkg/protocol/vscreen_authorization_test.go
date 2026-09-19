package protocol

import (
	"testing"
	"time"
)

func TestMirrorAuthorizationRequestValidation(t *testing.T) {
	request := MirrorAuthorizationRequest{
		Type:      MirrorAuthorizationRequestType,
		RequestID: "request-1",
		Kind:      "cli",
		Title:     "Codex needs sign-in",
		Message:   "Approve the login on this runtime?",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := request.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	request.Kind = "unknown"
	if err := request.Validate(time.Now()); err == nil {
		t.Fatal("unknown authorization kind was accepted")
	}
}

func TestMirrorAuthorizationResponseValidation(t *testing.T) {
	response := MirrorAuthorizationResponse{Type: MirrorAuthorizationResponseType, RequestID: "request-1", Approved: true}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	response.RequestID = ""
	if err := response.Validate(); err == nil {
		t.Fatal("response without request identity was accepted")
	}
}
