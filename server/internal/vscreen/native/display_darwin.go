//go:build darwin && cgo

package native

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -fblocks
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics -framework ScreenCaptureKit
#include "display.h"
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"unsafe"
)

func fromNative(d C.VSDisplay) Display {
	return Display{ID: uint32(d.id), Name: C.GoString(&d.name[0]), Builtin: d.builtin != 0, Managed: d.managed != 0, LogicalWidth: float64(d.logical_width), LogicalHeight: float64(d.logical_height), Scale: float64(d.scale), Main: d.main != 0, MirrorOf: uint32(d.mirror), UUID: C.GoString(&d.uuid[0]), X: int32(d.x), Y: int32(d.y), Width: uint32(d.width), Height: uint32(d.height), ScreenRecording: d.recording != 0, CaptureVisible: d.capture != 0}
}
func createDisplay(name string, serial, width, height uint32) (Display, error) {
	n := C.CString(name)
	defer C.free(unsafe.Pointer(n))
	var d C.VSDisplay
	if result := C.vs_create(n, C.uint32_t(serial), C.uint32_t(width), C.uint32_t(height), &d); result != 0 {
		return Display{}, fmt.Errorf("%w: create code %d", ErrUnavailable, result)
	}
	return fromNative(d), nil
}
func describeDisplay(id uint32) (Display, error) {
	var d C.VSDisplay
	if C.vs_describe(C.uint32_t(id), &d) != 0 {
		return Display{}, ErrUnavailable
	}
	return fromNative(d), nil
}
func listDisplays() ([]Display, error) {
	var ds [128]C.VSDisplay
	var count C.uint32_t
	if C.vs_list(&ds[0], 128, &count) != 0 {
		return nil, ErrUnavailable
	}
	result := make([]Display, 0, count)
	for i := uint32(0); i < uint32(count); i++ {
		result = append(result, fromNative(ds[i]))
	}
	return result, nil
}
func disposeDisplay(id uint32) error {
	if C.vs_dispose(C.uint32_t(id)) != 0 {
		return ErrUnavailable
	}
	return nil
}
func supported() bool { return C.vs_supported() != 0 }
func runLoop()        { C.vs_run() }
func stopLoop()       { C.vs_stop() }
