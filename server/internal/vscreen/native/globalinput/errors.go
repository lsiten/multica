package globalinput

import "errors"

// ErrUnsupported indicates global input is unavailable on this host build.
var ErrUnsupported = errors.New("globalinput: unsupported platform")

// ErrPermissionDenied indicates the host accessibility grant is missing.
var ErrPermissionDenied = errors.New("globalinput: accessibility permission denied")

// ErrUnavailable indicates posting an event failed on the host.
var ErrUnavailable = errors.New("globalinput: event posting unavailable")
