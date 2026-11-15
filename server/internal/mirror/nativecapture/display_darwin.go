//go:build darwin && cgo

package nativecapture

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -fblocks
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics
#include "display.h"
*/
import "C"

func fromNative(display C.MMRDisplay) Display {
	return Display{
		ID:            uint32(display.id),
		Name:          C.GoString(&display.name[0]),
		Builtin:       display.builtin != 0,
		LogicalWidth:  float64(display.logical_width),
		LogicalHeight: float64(display.logical_height),
		Scale:         float64(display.scale),
		Main:          display.main != 0,
		UUID:          C.GoString(&display.uuid[0]),
		X:             int32(display.x),
		Y:             int32(display.y),
		Width:         uint32(display.width),
		Height:        uint32(display.height),
	}
}

func listDisplays() ([]Display, error) {
	var displays [128]C.MMRDisplay
	var count C.uint32_t
	if C.mmr_list(&displays[0], 128, &count) != 0 {
		return nil, ErrUnavailable
	}
	result := make([]Display, 0, count)
	for index := uint32(0); index < uint32(count); index++ {
		result = append(result, fromNative(displays[index]))
	}
	return result, nil
}

func supported() bool { return C.mmr_supported() != 0 }
func runLoop()        { C.mmr_run() }
func stopLoop()       { C.mmr_stop() }
