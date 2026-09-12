package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMirrorOfferPayload_JSONShape(t *testing.T) {
	// Given
	payload := MirrorOfferPayload{
		SessionID:   "session-opaque",
		WorkspaceID: "workspace-1",
		RuntimeID:   "runtime-1",
		UserID:      "user-1",
		DaemonID:    "daemon-1",
		ViewerID:    "viewer-1",
		Offer: MirrorSessionDescription{
			Type: "offer",
			SDP:  "sdp-is-wire-data",
		},
		ExpiresAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}

	// When
	encoded, err := json.Marshal(payload)

	// Then
	if err != nil {
		t.Fatalf("marshal mirror offer: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode mirror offer: %v", err)
	}
	for _, field := range []string{"session_id", "workspace_id", "runtime_id", "viewer_id", "offer"} {
		if _, ok := fields[field]; !ok {
			t.Errorf("missing JSON field %q in %s", field, encoded)
		}
	}
	for _, field := range []string{"user_id", "daemon_id", "expires_at"} {
		if _, ok := fields[field]; !ok {
			t.Errorf("missing JSON field %q in %s", field, encoded)
		}
	}
	if got := string(fields["offer"]); got != `{"type":"offer","sdp":"sdp-is-wire-data"}` {
		t.Errorf("offer JSON = %s", got)
	}
}

func TestMirrorAnswerPayload_JSONShape(t *testing.T) {
	// Given
	payload := MirrorAnswerPayload{
		SessionID: "session-opaque",
		Answer:    MirrorSessionDescription{Type: "answer", SDP: "answer-sdp"},
		ExpiresAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}

	// When
	encoded, err := json.Marshal(payload)

	// Then
	if err != nil {
		t.Fatalf("marshal mirror answer: %v", err)
	}
	var decoded MirrorAnswerPayload
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal mirror answer: %v", err)
	}
	if decoded != payload {
		t.Fatalf("decoded answer = %+v, want %+v", decoded, payload)
	}
}

func TestMirrorAnswerFailurePayloadValidate(t *testing.T) {
	tests := []struct {
		name    string
		payload MirrorAnswerFailurePayload
		wantErr bool
	}{
		{
			name: "accepts fixed failure reasons",
			payload: MirrorAnswerFailurePayload{
				SessionID:   "session-1",
				WorkspaceID: "workspace-1",
				RuntimeID:   "runtime-1",
				UserID:      "user-1",
				DaemonID:    "daemon-1",
				ViewerID:    "viewer-1",
				Reason:      MirrorAnswerFailurePermissionDenied,
				ExpiresAt:   time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
			},
		},
		{
			name: "rejects unknown reason",
			payload: MirrorAnswerFailurePayload{
				SessionID:   "session-1",
				WorkspaceID: "workspace-1",
				RuntimeID:   "runtime-1",
				UserID:      "user-1",
				DaemonID:    "daemon-1",
				ViewerID:    "viewer-1",
				Reason:      "internal-error",
				ExpiresAt:   time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
			},
			wantErr: true,
		},
		{
			name: "rejects incomplete identity",
			payload: MirrorAnswerFailurePayload{
				SessionID:   "session-1",
				WorkspaceID: "workspace-1",
				RuntimeID:   "runtime-1",
				UserID:      "user-1",
				DaemonID:    "daemon-1",
				Reason:      MirrorAnswerFailureNegotiation,
				ExpiresAt:   time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
			},
			wantErr: true,
		},
		{
			name: "rejects missing expiry",
			payload: MirrorAnswerFailurePayload{
				SessionID:   "session-1",
				WorkspaceID: "workspace-1",
				RuntimeID:   "runtime-1",
				UserID:      "user-1",
				DaemonID:    "daemon-1",
				ViewerID:    "viewer-1",
				Reason:      MirrorAnswerFailureNegotiation,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.payload.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("Validate() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestMirrorAnswerFailurePayloadJSONHasNoSDP(t *testing.T) {
	// Given
	payload := MirrorAnswerFailurePayload{
		SessionID:   "session-1",
		WorkspaceID: "workspace-1",
		RuntimeID:   "runtime-1",
		UserID:      "user-1",
		DaemonID:    "daemon-1",
		ViewerID:    "viewer-1",
		Reason:      MirrorAnswerFailureNoDisplay,
		ExpiresAt:   time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}

	// When
	encoded, err := json.Marshal(payload)

	// Then
	if err != nil {
		t.Fatalf("marshal answer failure: %v", err)
	}
	if strings.Contains(string(encoded), "sdp") || strings.Contains(string(encoded), "answer") {
		t.Fatalf("failure JSON leaked negotiation data: %s", encoded)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode answer failure: %v", err)
	}
	if got := string(fields["reason"]); got != `"no-display"` {
		t.Fatalf("reason = %s, want %q", got, MirrorAnswerFailureNoDisplay)
	}
}
