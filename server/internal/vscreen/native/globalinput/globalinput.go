// Package globalinput injects human pointer and keyboard events into the
// physical host session (as opposed to appcontrol's per-PID background
// injection for the managed virtual screen).
//
// Global injection moves the real cursor and requires the host accessibility
// grant (macOS Accessibility / the equivalent Windows privilege). It is only
// reachable when the runtime owner has enabled remote interaction.
package globalinput

import "github.com/multica-ai/multica/server/pkg/protocol"

// Button mirrors the wire pointer buttons.
type Button string

const (
	ButtonLeft   Button = "left"
	ButtonMiddle Button = "middle"
	ButtonRight  Button = "right"
)

// PointerEvent is one physical pointer action in absolute target-display
// pixels. Coordinates have already been transformed from frame pixels.
type PointerEvent struct {
	Kind   protocol.MirrorInputKind
	Button Button
	X, Y   float64
	DeltaX float64
	DeltaY float64
}

// KeyEvent is one named-key press. Down=false is a key-up.
type KeyEvent struct {
	Key       string
	Modifiers []string
	Down      bool
}

// Injector posts events to the foreground session. Implementations must release
// any pressed button/key on Close and never touch the pasteboard.
type Injector interface {
	// Available reports whether global injection is compiled and the host
	// accessibility permission is currently granted.
	Available() bool
	Pointer(event PointerEvent) error
	Key(event KeyEvent) error
	// Text enters semantic Unicode at the current focus without clipboard use.
	Text(text string) error
	Close() error
}
