//go:build darwin && cgo

package appcontrol

/*
#include "bridge.h"
*/
import "C"
import "unsafe"

func currentPIDInputSource() string {
	source := C.ac_input_source()
	if source == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(source))
	return C.GoString(source)
}
