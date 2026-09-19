package globalinput

// virtualScreenMetrics describes the Win32 virtual desktop in global logical
// pixels. Origin is often negative on a secondary display placed left/up.
type virtualScreenMetrics struct {
	OriginX int32
	OriginY int32
	Width   int32
	Height  int32
}

// absoluteVirtualDesktopCoordinates maps global desktop pixels to SendInput's
// 0..65535 virtual-desktop normalized coordinate space.
func absoluteVirtualDesktopCoordinates(x, y float64, m virtualScreenMetrics) (int32, int32, bool) {
	if m.Width <= 1 || m.Height <= 1 {
		return 0, 0, false
	}
	dx := int32((x-float64(m.OriginX))*65535/float64(m.Width-1) + 0.5)
	dy := int32((y-float64(m.OriginY))*65535/float64(m.Height-1) + 0.5)
	return dx, dy, true
}
