//go:build darwin && cgo

package appcontrol

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -fblocks
#cgo LDFLAGS: -framework AppKit -framework Security -framework Carbon -framework ApplicationServices -framework ScreenCaptureKit -framework ImageIO -framework UniformTypeIdentifiers
#include "bridge.h"
*/
import "C"
import (
	"context"
	"encoding/json"
	"time"
	"unsafe"
)

type nativeBackend struct{ handle C.uintptr_t }

func newBackend() (backend, error) {
	h := C.ac_new()
	if h == 0 {
		return nil, refusal("native_unavailable")
	}
	return &nativeBackend{h}, nil
}
func (b *nativeBackend) call(ctx context.Context, operation string, input, output any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		Operation string
		Input     any
	}{operation, input})
	if err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(operationLimit)
	}
	request := C.ac_request_new(C.double(time.Until(deadline).Seconds()))
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { C.ac_request_cancel(request); close(stopped) })
	defer func() {
		if !stop() {
			<-stopped
		}
		C.ac_request_free(request)
	}()
	body := C.CBytes(raw)
	defer C.free(body)
	var result *C.char
	var size C.size_t
	if C.ac_call(b.handle, request, (*C.char)(body), C.size_t(len(raw)), &result, &size) != 0 {
		return refusal("native_unavailable")
	}
	defer C.free(unsafe.Pointer(result))
	if size > 12*1024*1024 {
		return refusal("invalid_native_reply")
	}
	var reply struct {
		Error string
		Value json.RawMessage
	}
	if err = json.Unmarshal(C.GoBytes(unsafe.Pointer(result), C.int(size)), &reply); err != nil {
		return refusal("invalid_native_reply")
	}
	if reply.Error != "" {
		switch reply.Error {
		case "accessibility_denied", "screen_recording_denied", "needs_intervention", "stale_window", "source_gone", "action_uncertain", "native_unavailable", "invalid_launch", "app_claim_conflict", "closed":
			return refusal(reply.Error)
		default:
			return refusal("native_unavailable")
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if output != nil {
		if err = json.Unmarshal(reply.Value, output); err != nil {
			return refusal("invalid_native_reply")
		}
	}
	return nil
}
func (b *nativeBackend) close() error { C.ac_free(b.handle); b.handle = 0; return nil }
