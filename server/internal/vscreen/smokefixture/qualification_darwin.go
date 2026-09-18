//go:build darwin && cgo

package smokefixture

/*
#cgo LDFLAGS: -framework Carbon -framework ApplicationServices -framework Security
#include <stdlib.h>
int multica_input_qualification_run(const char *);
char *multica_input_qualification_source(void);
char *multica_input_qualification_code(const char *);
*/
import "C"
import (
	"errors"
	"unsafe"
)

func runQualification(raw []byte) error {
	p := C.CString(string(raw))
	defer C.free(unsafe.Pointer(p))
	if C.multica_input_qualification_run(p) != 0 {
		return errors.New("qualification_fixture_failed")
	}
	return nil
}
func QualificationInputSource() string {
	p := C.multica_input_qualification_source()
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}

func QualificationCodeHash(path string) string {
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	raw := C.multica_input_qualification_code(p)
	if raw == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(raw))
	return C.GoString(raw)
}
