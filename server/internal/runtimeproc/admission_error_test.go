package runtimeproc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNotSubmittedErrorsDoNotCreateReceipts(t *testing.T) {
	_, client, _, _ := startService(t, Config{Bootstrap: testBootstrap(t)})
	for _, mode := range []string{"missing_deadline", "malformed_payload", "oversized_request"} {
		t.Run(mode, func(t *testing.T) {
			request, err := client.Request("resume", Fence{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing_deadline":
				request.Deadline = time.Time{}
			case "malformed_payload":
				request.Payload = json.RawMessage(`{`)
			case "oversized_request":
				request.Payload = json.RawMessage(`"` + strings.Repeat("x", maxBody) + `"`)
			}
			_, err = client.Call(t.Context(), request)
			var local *NotSubmittedError
			if !errors.As(err, &local) || !RejectedBeforeAdmission(err) {
				t.Fatalf("local refusal lost submission classification: %v", err)
			}
			status, err := client.Health(t.Context())
			if err != nil || status.Receipts.Total != 0 {
				t.Fatalf("local validation admitted a receipt: %+v %v", status.Receipts, err)
			}
		})
	}
}

func TestSubmissionRefusalsExcludeUncertainEffects(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("connection lost"), &Error{Code: "storage"}, &Error{Code: "environment_failed"}} {
		if RejectedBeforeAdmission(err) {
			t.Fatalf("unknown effect classified as safe to retry: %v", err)
		}
	}
	for _, code := range []string{"stale_fence", "retired_request", "resource_exhausted", "payload_too_large", "malformed", "deadline"} {
		if !RejectedBeforeAdmission(&Error{Code: code}) {
			t.Fatalf("explicit submission refusal not classified: %s", code)
		}
	}
}
