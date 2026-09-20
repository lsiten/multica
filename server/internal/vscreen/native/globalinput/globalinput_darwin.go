//go:build cgo && darwin

package globalinput

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>
#include <stdbool.h>

static bool gi_trusted(void) { return AXIsProcessTrusted(); }

static int32_t gi_mouse(int kind, CGFloat x, CGFloat y, int button, int64_t dx, int64_t dy) {
	CGPoint p = CGPointMake(x, y);
	CGEventRef e = NULL;
	CGEventType t = 0;
	switch (kind) {
		case 1:
			t = button == 1 ? kCGEventLeftMouseDragged : button == 2 ? kCGEventOtherMouseDragged : button == 3 ? kCGEventRightMouseDragged : kCGEventMouseMoved;
			break;
		case 2:
			t = (button == 1) ? kCGEventLeftMouseDown : (button == 2) ? kCGEventOtherMouseDown : kCGEventRightMouseDown;
			break;
		case 3:
			t = (button == 1) ? kCGEventLeftMouseUp : (button == 2) ? kCGEventOtherMouseUp : kCGEventRightMouseUp;
			break;
		case 4:
			e = CGEventCreateScrollWheelEvent2(NULL, kCGScrollEventUnitPixel, 2, (int32_t)dy, (int32_t)dx, 0);
			if (e == NULL) return -2;
			CGEventSetLocation(e, p);
			CGEventSetFlags(e, 0);
			CGEventPost(kCGHIDEventTap, e);
			CFRelease(e);
			return 0;
		default: return -1;
	}
	e = CGEventCreateMouseEvent(NULL, t, p, (button==2)?kCGMouseButtonCenter:(button==3)?kCGMouseButtonRight:kCGMouseButtonLeft);
	if (e == NULL) return -2;
	CGEventSetFlags(e, 0);
	CGEventPost(kCGHIDEventTap, e);
	CFRelease(e);
	return 0;
}

static void gi_key(CGKeyCode code, bool down, CGEventFlags flags) {
	CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStatePrivate);
	CGEventRef e = CGEventCreateKeyboardEvent(src, code, down);
	if (e != NULL) {
		CGEventSetFlags(e, flags);
		CGEventPost(kCGHIDEventTap, e);
		CFRelease(e);
	}
	if (src != NULL) CFRelease(src);
}

static void gi_unicode(const uint16_t *chars, size_t len) {
	CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStatePrivate);
	for (size_t i = 0; i < len;) {
		size_t units = 1;
		if (chars[i] >= 0xD800 && chars[i] <= 0xDBFF && i+1 < len && chars[i+1] >= 0xDC00 && chars[i+1] <= 0xDFFF) units = 2;
		CGEventRef down = CGEventCreateKeyboardEvent(src, 0, true);
		CGEventRef up   = CGEventCreateKeyboardEvent(src, 0, false);
		if (down != NULL) { CGEventSetFlags(down, 0); CGEventKeyboardSetUnicodeString(down, units, chars+i); CGEventPost(kCGHIDEventTap, down); CFRelease(down); }
		if (up != NULL)   { CGEventSetFlags(up, 0); CGEventKeyboardSetUnicodeString(up, units, chars+i); CGEventPost(kCGHIDEventTap, up); CFRelease(up); }
		i += units;
	}
	if (src != NULL) CFRelease(src);
}
*/
import "C"

import (
	"unicode/utf16"
	"unsafe"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type darwinInjector struct{}

// New returns the CoreGraphics-backed global injector for macOS.
func New() Injector { return &darwinInjector{} }

func (darwinInjector) Available() bool { return bool(C.gi_trusted()) }
func (darwinInjector) Close() error    { return nil }

// pointer kinds used only in the C shim: 1 move, 2 down, 3 up, 4 wheel.
func pointerKind(k protocol.MirrorInputKind) C.int {
	switch k {
	case protocol.MirrorInputPointerMove:
		return 1
	case protocol.MirrorInputPointerDown:
		return 2
	case protocol.MirrorInputPointerUp:
		return 3
	case protocol.MirrorInputWheel:
		return 4
	default:
		return 0
	}
}

func buttonNumber(b Button) C.int {
	switch protocol.MirrorPointerButton(b) {
	case protocol.MirrorButtonMiddle:
		return 2
	case protocol.MirrorButtonRight:
		return 3
	case protocol.MirrorButtonLeft:
		return 1
	default:
		return 0
	}
}

func (i darwinInjector) Pointer(event PointerEvent) error {
	if !i.Available() {
		return ErrPermissionDenied
	}
	kind := pointerKind(event.Kind)
	if kind == 0 {
		return ErrUnsupported
	}
	rc := C.gi_mouse(kind, C.CGFloat(event.X), C.CGFloat(event.Y),
		buttonNumber(event.Button), C.int64_t(event.DeltaX), C.int64_t(event.DeltaY))
	if rc != 0 {
		return ErrUnavailable
	}
	return nil
}

var darwinKeyCodes = map[string]C.ushort{
	"a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9,
	"b": 11, "q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17,
	"1": 18, "2": 19, "3": 20, "4": 21, "6": 22, "5": 23,
	"=": 24, "9": 25, "7": 26, "-": 27, "8": 28, "0": 29,
	"o": 31, "u": 32, "i": 33, "p": 35, "l": 37, "j": 38, "k": 40, "n": 45, "m": 46,
	"return": 36, "enter": 36, "tab": 48, "space": 49, "backspace": 51, "delete": 117,
	"escape": 53, "arrowleft": 123, "arrowright": 124, "arrowdown": 125, "arrowup": 126,
}

func darwinKeyCode(key string) (C.ushort, bool) {
	code, ok := darwinKeyCodes[key]
	return code, ok
}

func modifierFlags(mods []string) C.CGEventFlags {
	var flags C.CGEventFlags
	for _, mod := range mods {
		switch mod {
		case "shift":
			flags |= C.kCGEventFlagMaskShift
		case "control":
			flags |= C.kCGEventFlagMaskControl
		case "alt":
			flags |= C.kCGEventFlagMaskAlternate
		case "meta":
			flags |= C.kCGEventFlagMaskCommand
		}
	}
	return flags
}

func (i darwinInjector) Key(event KeyEvent) error {
	if !i.Available() {
		return ErrPermissionDenied
	}
	code, ok := darwinKeyCode(event.Key)
	if !ok {
		return ErrUnsupported
	}
	down := C.bool(true)
	if !event.Down {
		down = C.bool(false)
	}
	C.gi_key(C.CGKeyCode(code), down, modifierFlags(event.Modifiers))
	return nil
}

func (i darwinInjector) Text(text string) error {
	if !i.Available() {
		return ErrPermissionDenied
	}
	encoded := utf16.Encode([]rune(text))
	if len(encoded) == 0 {
		return nil
	}
	C.gi_unicode((*C.uint16_t)(unsafe.Pointer(&encoded[0])), C.size_t(len(encoded)))
	return nil
}
