package native

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestQualificationCompletionRequiresFinalTargetACKAndEffect(t *testing.T) {
	for _, failure := range []string{"", "token", "event", "counter_only", "window", "pid", "binding", "cancelled", "expired"} {
		t.Run(failure, func(t *testing.T) {
			q, p, action := qualificationFixture(t)
			if !q.decide(p, action).Certified {
				t.Fatal("fixture policy refused")
			}
			request := protocol.VscreenActionRequest{ActionID: "complete", Sequence: 1, Target: protocol.VscreenActionTarget{Resource: q.scope.Resource, WindowHandle: q.window.Handle, SnapshotRevision: 1}, Action: action}
			q.pending = map[string]protocol.VscreenActionRequest{request.ActionID: request}
			window := q.window
			window.SnapshotRevision = 1
			completion := appcontrol.PIDCompletion{Binding: appcontrol.PIDCompletionBinding{Token: math.MaxUint64 - 1, Target: request.Target, ActionID: request.ActionID, Sequence: 1, Process: q.window.Process}, Action: action, Window: window}
			state := smokefixture.QualificationState{State: smokefixture.State{Nonce: q.scope.Nonce, PID: p.PID, ProcessStart: p.Start, WindowID: q.window.WindowID, DisplayID: q.window.DisplayID, Text: action.Type.Text}, LastProcessedToken: completion.Binding.Token, LastEventType: 11, SelectionLocation: uint64(len(utf16.Encode([]rune(action.Type.Text))))}
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
			defer cancel()
			switch failure {
			case "token":
				state.LastProcessedToken--
			case "event":
				state.LastEventType = 10
			case "counter_only":
				state.Text = ""
				state.Keys++
			case "window":
				state.WindowID++
			case "pid":
				state.PID++
			case "binding":
				completion.Binding.Target.Epoch.GeometryRevision++
			case "cancelled":
				cancel()
			case "expired":
				q.expires = time.Now().Add(-time.Second)
			}
			raw, _ := json.Marshal(state)
			if err := os.WriteFile(filepath.Join(q.scope.Directory, "readback.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			receipt, err := q.verifyCompletion(ctx, completion)
			if (err == nil) != (failure == "") {
				t.Fatalf("%s: %v", failure, err)
			}
			if failure == "" && (receipt.Binding != completion.Binding || !receipt.TargetProcessed || !receipt.EffectVerified) {
				t.Fatal("incomplete receipt")
			}
			if failure != "" && (receipt.TargetProcessed || receipt.EffectVerified) {
				t.Fatal("uncertain ACK cleared")
			}
		})
	}
}
