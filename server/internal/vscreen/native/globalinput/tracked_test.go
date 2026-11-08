package globalinput

import (
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type postingFixture struct {
	pointers []PointerEvent
	keys     []KeyEvent
	fail     bool
}

func (f *postingFixture) Available() bool   { return true }
func (f *postingFixture) Close() error      { return nil }
func (f *postingFixture) Text(string) error { return nil }
func (f *postingFixture) Pointer(e PointerEvent) error {
	if f.fail {
		return ErrUnavailable
	}
	f.pointers = append(f.pointers, e)
	return nil
}
func (f *postingFixture) Key(e KeyEvent) error {
	if f.fail {
		return ErrUnavailable
	}
	f.keys = append(f.keys, e)
	return nil
}

func TestTrackedInjectorCloseReleasesSuccessfulDownsOnce(t *testing.T) {
	fixture := &postingFixture{}
	injector := newTrackedInjector(fixture)
	if err := injector.Pointer(PointerEvent{Kind: protocol.MirrorInputPointerDown, Button: ButtonLeft, X: 10, Y: 20}); err != nil {
		t.Fatal(err)
	}
	if err := injector.Key(KeyEvent{Key: "a", Down: true, Modifiers: []string{"shift"}}); err != nil {
		t.Fatal(err)
	}
	fixture.fail = true
	if err := injector.Key(KeyEvent{Key: "b", Down: true}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed post = %v", err)
	}
	fixture.fail = false
	if err := injector.Close(); err != nil {
		t.Fatal(err)
	}
	if err := injector.Close(); err != nil {
		t.Fatal(err)
	}
	if len(fixture.pointers) != 2 || fixture.pointers[1].Kind != protocol.MirrorInputPointerUp || fixture.pointers[1].X != 10 {
		t.Fatalf("pointer posts = %+v", fixture.pointers)
	}
	if len(fixture.keys) != 2 || fixture.keys[1].Down || fixture.keys[1].Key != "a" || len(fixture.keys[1].Modifiers) != 0 {
		t.Fatalf("key posts = %+v", fixture.keys)
	}
}
