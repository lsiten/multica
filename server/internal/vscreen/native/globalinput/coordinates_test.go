package globalinput

import "testing"

func TestAbsoluteVirtualDesktopCoordinates(t *testing.T) {
	metrics := virtualScreenMetrics{OriginX: -1440, OriginY: -100, Width: 3360, Height: 1100}
	tests := []struct {
		name         string
		x, y         float64
		wantX, wantY int32
	}{
		{name: "virtual origin", x: -1440, y: -100, wantX: 0, wantY: 0},
		{name: "virtual far edge", x: 1919, y: 999, wantX: 65535, wantY: 65535},
		{name: "primary origin", x: 0, y: 0, wantX: 28095, wantY: 5963},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotX, gotY, ok := absoluteVirtualDesktopCoordinates(tt.x, tt.y, metrics)
			if !ok {
				t.Fatal("coordinates rejected")
			}
			if gotX != tt.wantX || gotY != tt.wantY {
				t.Fatalf("coordinates = (%d,%d), want (%d,%d)", gotX, gotY, tt.wantX, tt.wantY)
			}
		})
	}
}

func TestAbsoluteVirtualDesktopCoordinatesRejectsInvalidMetrics(t *testing.T) {
	if _, _, ok := absoluteVirtualDesktopCoordinates(0, 0, virtualScreenMetrics{}); ok {
		t.Fatal("invalid metrics accepted")
	}
}
