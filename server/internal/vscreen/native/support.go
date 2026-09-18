package native

// Supported probes the required virtual-display selectors without creating a
// display, initializing AppKit, or requesting Screen Recording permission.
func Supported() bool { return supported() }
