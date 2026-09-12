package mirror

import (
	"context"
	"image"
	"runtime"
	"strings"

	"github.com/kbinani/screenshot"
)

// NativeCapturer reads the primary active display through the host operating
// system. On macOS the daemon needs Screen Recording permission. Linux capture
// is intentionally unsupported in the first mirror version.
type NativeCapturer struct{}

func (NativeCapturer) Capture(context.Context) (image.Image, error) {
	if !NativeCaptureSupported() {
		return nil, ErrUnsupportedPlatform
	}
	if screenshot.NumActiveDisplays() == 0 {
		return nil, ErrNoDisplay
	}
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, classifyNativeCaptureError(err)
	}
	if img == nil {
		return nil, ErrCaptureUnavailable
	}
	return img, nil
}

func classifyNativeCaptureError(err error) error {
	message := err.Error()
	if runtime.GOOS == "darwin" && strings.Contains(message, "cannot capture display") {
		return ErrCapturePermissionDenied
	}
	return wrapCaptureError(err)
}
