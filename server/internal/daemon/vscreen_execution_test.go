package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestVscreenProviderStopWaitsForResultAndPreservesUsage(t *testing.T) {
	for _, status := range []string{"aborted", "completed"} {
		t.Run(status, func(t *testing.T) {
			d, _ := newTranscriptRecorder(t)
			messages := make(chan agent.Message)
			result := make(chan agent.Result, 1)
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			returned := make(chan agent.Result, 1)
			failure := make(chan error, 1)
			go func() {
				value, _, err := d.executeAndDrain(ctx, sessionBackend{session: &agent.Session{Messages: messages, Result: result}}, "fixture", agent.ExecOptions{}, slog.Default(), "task", "", new(atomic.Int32))
				returned <- value
				failure <- err
			}()
			messages <- agent.Message{Type: agent.MessageText, Content: "owned provider fixture"}
			cancel(errVscreenIntervention)
			select {
			case <-returned:
				t.Fatal("returned before provider terminal cleanup")
			case <-time.After(20 * time.Millisecond):
			}
			close(messages)
			result <- agent.Result{Status: status, SessionID: "owned-session", Usage: map[string]agent.TokenUsage{"fixture": {InputTokens: 17}}}
			close(result)
			select {
			case value := <-returned:
				if err := <-failure; err != nil {
					t.Fatal(err)
				}
				if value.Status != status || value.SessionID != "owned-session" || value.Usage["fixture"].InputTokens != 17 {
					t.Fatalf("terminal result corrupted: %+v", value)
				}
			case <-time.After(time.Second):
				t.Fatal("provider stop did not finish")
			}
		})
	}
}

func TestVscreenProviderTranscriptOmitsNativePixelsAndText(t *testing.T) {
	d, rec := newTranscriptRecorder(t)
	messages := make(chan agent.Message, 2)
	messages <- agent.Message{Type: agent.MessageToolUse, Tool: "mcp__multica-vscreen__vscreen_type", CallID: "gui", Input: map[string]any{"text": "private-input-marker"}}
	messages <- agent.Message{Type: agent.MessageToolResult, CallID: "gui", Output: `{"content":[{"type":"image","data":"pixel-base64-marker"},{"type":"text","text":"private-ax-marker"}]}`}
	close(messages)
	results := make(chan agent.Result, 1)
	results <- agent.Result{Status: "completed"}
	close(results)
	_, _, err := d.executeAndDrain(context.Background(), sessionBackend{session: &agent.Session{Messages: messages, Result: results}}, "fixture", agent.ExecOptions{}, slog.Default(), "task", "", new(atomic.Int32))
	if err != nil {
		t.Fatal(err)
	}
	captured, err := json.Marshal(rec.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"private-input-marker", "pixel-base64-marker", "private-ax-marker"} {
		if bytes.Contains(captured, []byte(marker)) {
			t.Fatalf("managed native payload leaked into task transcript: %s", marker)
		}
	}
	if len(rec.snapshot()) != 2 {
		t.Fatal("action summaries missing")
	}
}

func TestVscreenProviderStopFinalizationPrecedence(t *testing.T) {
	for _, scenario := range []string{"stopped", "completed", "user_cancel", "unconfirmed"} {
		t.Run(scenario, func(t *testing.T) {
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			execution, stop := context.WithCancelCause(parent)
			defer stop(nil)
			stop(errVscreenIntervention)
			result := TaskResult{Status: "cancelled", SessionID: "session", WorkDir: "owned-workdir", Usage: []TaskUsageEntry{{}}}
			var runErr error
			if scenario == "completed" {
				result.Status = "completed"
			}
			if scenario == "user_cancel" {
				cancelParent()
			}
			if scenario == "unconfirmed" {
				runErr = errVscreenStopUnconfirmed
			}
			finalizeVscreenStop(parent, execution, &result, &runErr)
			if scenario == "stopped" {
				if result.Status != "blocked" || result.FailureReason != "gui_human_intervention" {
					t.Fatal(result)
				}
			} else if result.FailureReason != "" {
				t.Fatal("cancel/completed/unconfirmed was rewritten")
			}
			if result.SessionID != "session" || result.WorkDir != "owned-workdir" || len(result.Usage) != 1 {
				t.Fatal("delivery metadata lost")
			}
		})
	}
}
