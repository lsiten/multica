//go:build darwin && cgo

package smokefixture

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -fblocks
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics -framework CoreServices
#include "bridge.h"
#include <stdlib.h>
*/
import "C"
import (
	"encoding/json"
	"errors"
	"unsafe"
)

func register(path string) error {
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	if C.multica_smoke_fixture_register(p) != 0 {
		return errors.New("fixture_registration_failed")
	}
	return nil
}
func processStart(pid int) (string, error) {
	p := C.multica_smoke_fixture_start(C.int(pid))
	if p == nil {
		return "", errors.New("process_not_alive")
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p), nil
}
func run(raw []byte) error {
	p := C.CString(string(raw))
	defer C.free(unsafe.Pointer(p))
	if C.multica_smoke_fixture_run(p) != 0 {
		return errors.New("fixture_failed")
	}
	return nil
}
func snapshot() (Foreground, error) {
	p := C.multica_smoke_fixture_foreground()
	if p == nil {
		return Foreground{}, errors.New("foreground_snapshot_unavailable")
	}
	defer C.free(unsafe.Pointer(p))
	var state Foreground
	err := json.Unmarshal([]byte(C.GoString(p)), &state)
	return state, err
}
