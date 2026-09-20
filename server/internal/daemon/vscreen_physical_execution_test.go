package daemon

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/globalinput"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type physicalInjectorFixture struct {
	mu       sync.Mutex
	pointers []globalinput.PointerEvent
	keys     []globalinput.KeyEvent
	texts    []string
}

func (f *physicalInjectorFixture) Available() bool { return true }
func (f *physicalInjectorFixture) Close() error    { return nil }
func (f *physicalInjectorFixture) Pointer(event globalinput.PointerEvent) error {
	f.mu.Lock()
	f.pointers = append(f.pointers, event)
	f.mu.Unlock()
	return nil
}
func (f *physicalInjectorFixture) Key(event globalinput.KeyEvent) error {
	f.mu.Lock()
	f.keys = append(f.keys, event)
	f.mu.Unlock()
	return nil
}
func (f *physicalInjectorFixture) Text(text string) error {
	f.mu.Lock()
	f.texts = append(f.texts, text)
	f.mu.Unlock()
	return nil
}

func TestPhysicalVscreenExecutionUsesExplicitSourceAndGlobalInjector(t *testing.T) {
	key := protocol.ResourceKey{BackendIdentity: "https://backend.example", WorkspaceID: "ws", RuntimeID: "rt", UID: 501, DisplayID: 7}
	descriptor := native.SourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{
		Resource:    key,
		Source:      protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "display:physical"},
		NativeEpoch: "native", Generation: "generation",
	}, DisplayID: 7, Width: 1000, Height: 500, LogicalWidth: 1000, LogicalHeight: 500}
	injector := &physicalInjectorFixture{}
	execution := newPhysicalVscreenExecution(t.Context(), Task{ID: "task", WorkspaceID: "ws", RuntimeID: "rt", MirrorSource: &descriptor.MirrorSourceBinding}, descriptor, nil, injector, mirror.NewArbiter(), nil)
	t.Cleanup(execution.Close)

	acquire, err := execution.invoke(t.Context(), "vscreen_acquire", mustJSON(t, vscreenToolArgs{RequestID: "request", Intent: "operate"}))
	if err != nil || len(acquire) == 0 {
		t.Fatalf("acquire = %v, err = %v", acquire, err)
	}
	var acquirePayload map[string]any
	if err := json.Unmarshal([]byte(acquire[0]["text"].(string)), &acquirePayload); err != nil {
		t.Fatal(err)
	}
	tx, ok := acquirePayload["transaction_id"].(string)
	if !ok || tx == "" {
		t.Fatalf("missing transaction id: %#v", acquirePayload)
	}

	click := vscreenToolArgs{TransactionID: tx, ActionID: "click", Sequence: 1, Action: &protocol.VscreenAction{Kind: protocol.VscreenActionClick, Click: &protocol.VscreenClickAction{Position: &protocol.VscreenPoint{X: 250, Y: 125}}}}
	if _, err := execution.invoke(t.Context(), "vscreen_click", mustJSON(t, click)); err != nil {
		t.Fatalf("click: %v", err)
	}
	typeAction := vscreenToolArgs{TransactionID: tx, ActionID: "type", Sequence: 2, Action: &protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: "display-focus", Text: "hello"}}}
	if _, err := execution.invoke(t.Context(), "vscreen_type", mustJSON(t, typeAction)); err != nil {
		t.Fatalf("type: %v", err)
	}

	injector.mu.Lock()
	defer injector.mu.Unlock()
	if len(injector.pointers) != 2 || injector.pointers[0].Kind != protocol.MirrorInputPointerDown || injector.pointers[1].Kind != protocol.MirrorInputPointerUp {
		t.Fatalf("pointer events = %#v", injector.pointers)
	}
	if injector.pointers[0].X != 250 || injector.pointers[0].Y != 125 {
		t.Fatalf("pointer coordinates = (%v,%v)", injector.pointers[0].X, injector.pointers[0].Y)
	}
	if len(injector.texts) != 1 || injector.texts[0] != "hello" {
		t.Fatalf("text events = %#v", injector.texts)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
