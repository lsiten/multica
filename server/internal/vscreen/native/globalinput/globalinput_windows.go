//go:build windows

package globalinput

import (
	"math"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procSendInput        = user32.NewProc("SendInput")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove        = 0x0001
	mouseAbsolute    = 0x8000
	mouseLeftDown    = 0x0002
	mouseLeftUp      = 0x0004
	mouseRightDown   = 0x0008
	mouseRightUp     = 0x0010
	mouseMiddleDown  = 0x0020
	mouseMiddleUp    = 0x0040
	mouseWheel       = 0x0800
	mouseHWheel      = 0x01000
	mouseVirtualDesk = 0x4000
	wheelDelta       = 120

	keyeventfKeyUp   = 0x0002
	keyeventfUnicode = 0x0004

	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCxVirtualScreen = 78
	smCyVirtualScreen = 79
)

// input matches the Win32 INPUT ABI on 64-bit. After type+padding (8 bytes),
// the anonymous union begins. The flat fields below line up with MOUSEINPUT:
// dx@0 dy@4 mouseData@8 flags@12 time@16 dwExtraInfo@24. KEYBDINPUT overlays
// the same bytes: wVk@0 wScan@2 dwFlags@4 time@8 dwExtraInfo@16, so keyboard
// constructors pack VK/scan into the first word pair and flags into Dy.
type input struct {
	Type      uint32
	_         uint32
	Dx        int32
	Dy        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	_         uint32
	Info      uintptr
}

type windowsInjector struct{}

// New returns the SendInput-backed global injector for Windows.
func New() Injector { return windowsInjector{} }

func (windowsInjector) Available() bool { return true }
func (windowsInjector) Close() error    { return nil }

func send(in []input) error {
	if len(in) == 0 {
		return nil
	}
	ret, _, _ := procSendInput.Call(uintptr(len(in)),
		uintptr(unsafe.Pointer(&in[0])), uintptr(unsafe.Sizeof(input{})))
	if int(ret) != len(in) {
		return ErrUnavailable
	}
	return nil
}

func systemVirtualScreen() virtualScreenMetrics {
	originX, _, _ := procGetSystemMetrics.Call(smXVirtualScreen)
	originY, _, _ := procGetSystemMetrics.Call(smYVirtualScreen)
	width, _, _ := procGetSystemMetrics.Call(smCxVirtualScreen)
	height, _, _ := procGetSystemMetrics.Call(smCyVirtualScreen)
	return virtualScreenMetrics{
		OriginX: int32(originX), OriginY: int32(originY),
		Width: int32(width), Height: int32(height),
	}
}

func (w windowsInjector) Pointer(event PointerEvent) error {
	metrics := systemVirtualScreen()
	dx, dy, ok := absoluteVirtualDesktopCoordinates(event.X, event.Y, metrics)
	if !ok {
		return ErrUnavailable
	}
	mouse := func(flags uint32, data uint32) input {
		return input{Type: inputMouse, Dx: dx, Dy: dy, MouseData: data, Flags: flags | mouseAbsolute | mouseVirtualDesk}
	}
	var events []input
	switch event.Kind {
	case protocol.MirrorInputPointerMove:
		events = []input{mouse(mouseMove, 0)}
	case protocol.MirrorInputPointerDown:
		switch protocol.MirrorPointerButton(event.Button) {
		case protocol.MirrorButtonRight:
			events = []input{mouse(mouseRightDown, 0)}
		case protocol.MirrorButtonMiddle:
			events = []input{mouse(mouseMiddleDown, 0)}
		default:
			events = []input{mouse(mouseLeftDown, 0)}
		}
	case protocol.MirrorInputPointerUp:
		switch protocol.MirrorPointerButton(event.Button) {
		case protocol.MirrorButtonRight:
			events = []input{mouse(mouseRightUp, 0)}
		case protocol.MirrorButtonMiddle:
			events = []input{mouse(mouseMiddleUp, 0)}
		default:
			events = []input{mouse(mouseLeftUp, 0)}
		}
	case protocol.MirrorInputWheel:
		if deltaY := wheelData(event.DeltaY); deltaY != 0 {
			events = append(events, mouse(mouseWheel, uint32(deltaY)))
		}
		if deltaX := wheelData(event.DeltaX); deltaX != 0 {
			events = append(events, mouse(mouseHWheel, uint32(deltaX)))
		}
		if len(events) == 0 {
			return nil
		}
	default:
		return ErrUnsupported
	}
	return send(events)
}

func wheelData(delta float64) int32 {
	if delta == 0 || math.IsNaN(delta) || math.IsInf(delta, 0) {
		return 0
	}
	value := int32(math.Round(delta))
	if value > 10*wheelDelta {
		value = 10 * wheelDelta
	} else if value < -10*wheelDelta {
		value = -10 * wheelDelta
	}
	return value
}

var windowsVK = map[string]uint16{
	"enter": 0x0D, "return": 0x0D, "tab": 0x09, "space": 0x20, "escape": 0x1B,
	"backspace": 0x08, "delete": 0x2E,
	"arrowleft": 0x25, "arrowup": 0x26, "arrowright": 0x27, "arrowdown": 0x28,
}

var windowsModifierVK = map[string]uint16{
	"shift": 0x10, "control": 0x11, "alt": 0x12, "meta": 0x5B,
}

func keyVK(key string) (uint16, bool) {
	if len(key) == 1 {
		c := key[0]
		switch {
		case c >= 'a' && c <= 'z':
			return uint16('A' + c - 'a'), true
		case c >= 'A' && c <= 'Z':
			return uint16(c), true
		case c >= '0' && c <= '9':
			return uint16(c), true
		}
	}
	vk, ok := windowsVK[key]
	return vk, ok
}

func vkKeyEvent(vk uint16, up bool) input {
	flags := uint32(0)
	if up {
		flags |= keyeventfKeyUp
	}
	// wVk occupies the low 16 bits of the Dx/word-pair; dwFlags overlays Dy.
	return input{Type: inputKeyboard, Dx: int32(vk), Dy: int32(flags)}
}

func (w windowsInjector) Key(event KeyEvent) error {
	events, err := keyInputEvents(event)
	if err != nil {
		return err
	}
	return send(events)
}

func keyInputEvents(event KeyEvent) ([]input, error) {
	vk, ok := keyVK(event.Key)
	if !ok {
		return nil, ErrUnsupported
	}
	modifierDowns, modifierVKs, err := modifierKeyEvents(event.Modifiers)
	if err != nil {
		return nil, err
	}
	events := make([]input, 0, len(modifierDowns)*2+1)
	events = append(events, modifierDowns...)
	events = append(events, vkKeyEvent(vk, !event.Down))
	if event.Down {
		// Keep modifiers pressed for the duration of a key-down gesture. Its
		// matching key-up arrives as a separate MirrorInputKeyUp message.
		return events, nil
	}
	for i := len(modifierVKs) - 1; i >= 0; i-- {
		events = append(events, vkKeyEvent(modifierVKs[i], true))
	}
	return events, nil
}

func modifierKeyEvents(modifiers []string) ([]input, []uint16, error) {
	events := make([]input, 0, len(modifiers))
	vks := make([]uint16, 0, len(modifiers))
	seen := make(map[string]bool, len(modifiers))
	for _, modifier := range modifiers {
		vk, ok := windowsModifierVK[modifier]
		if !ok {
			return nil, nil, ErrUnsupported
		}
		if seen[modifier] {
			continue
		}
		seen[modifier] = true
		events = append(events, vkKeyEvent(vk, false))
		vks = append(vks, vk)
	}
	return events, vks, nil
}

func unicodeKeyEvent(r uint16, up bool) input {
	flags := uint32(keyeventfUnicode)
	if up {
		flags |= keyeventfKeyUp
	}
	// wScan occupies the high 16 bits of the Dx/word-pair.
	return input{Type: inputKeyboard, Dx: int32(uint32(r) << 16), Dy: int32(flags)}
}

func (w windowsInjector) Text(text string) error {
	encoded := utf16.Encode([]rune(text))
	events := make([]input, 0, len(encoded)*2)
	for i := 0; i < len(encoded); i++ {
		if utf16.IsSurrogate(rune(encoded[i])) && i+1 < len(encoded) {
			high, low := encoded[i], encoded[i+1]
			events = append(events,
				unicodeKeyEvent(high, false),
				unicodeKeyEvent(low, false),
				unicodeKeyEvent(high, true),
				unicodeKeyEvent(low, true),
			)
			i++
			continue
		}
		events = append(events, unicodeKeyEvent(encoded[i], false), unicodeKeyEvent(encoded[i], true))
	}
	return send(events)
}
