package runtimeproc

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
)

func TestExplicitUnknownEffectsStayPendingWithoutContextCancellation(t *testing.T) {
	var calls atomic.Int32
	_, client, _, _ := startService(t, Config{Bootstrap: testBootstrap(t), Capabilities: []string{"work"}, Handler: func(ctx context.Context, _ Request) (json.RawMessage, *Error) {
		calls.Add(1)
		if ctx.Err() != nil {
			t.Error("fixture unexpectedly cancelled")
		}
		return json.RawMessage(`{"provisional":true}`), &Error{Code: CodeEffectsUnknown, Message: "unconfirmed external effect"}
	}})
	input := request(t, client, "work", `{}`)
	response, err := client.Call(t.Context(), input)
	if err != nil || response.Receipt == nil || response.Receipt.State != "pending" {
		t.Fatalf("unknown effect completed: %+v, error: %v", response, err)
	}
	receipt, err := client.QueryOperation(t.Context(), input.RequestID)
	if err != nil || receipt.State != "pending" || len(receipt.Result) != 0 {
		t.Fatalf("unconfirmed outcome consumed: %+v, error: %v", receipt, err)
	}
	repeated, err := client.Call(t.Context(), input)
	if err != nil || repeated.Receipt == nil || repeated.Receipt.State != "pending" {
		t.Fatalf("unknown operation lost its original pending receipt: %+v, error: %v", repeated, err)
	}
	if calls.Load() != 1 {
		t.Fatal("unknown operation was replayed")
	}
}
