package capture

import (
	"math"
	"testing"
)

func TestLayoutFitsWholeUltrawideDisplay(t *testing.T) {
	// Given / When: an ultrawide source in a 16:9 encoder output.
	layout, err := FitLayout(3440, 1440, 1600, 900)
	if err != nil {
		t.Fatal(err)
	}
	// Then: full width, centered vertical letterbox, preserved aspect ratio.
	if math.Abs(layout.ContentX) > 1e-9 || layout.ContentWidth != 1600 || layout.ContentY <= 0 || math.Abs(layout.ContentWidth/layout.ContentHeight-3440.0/1440) > 0.000001 {
		t.Fatalf("bad aspect fit %+v", layout)
	}
}

func TestLayoutPreservesRetinaLogicalCoordinates(t *testing.T) {
	// Given / When: logical dimensions, independent of a 2x backing scale.
	layout, err := FitLayout(1512, 982, 1600, 900)
	if err != nil {
		t.Fatal(err)
	}
	// Then: the full logical display is fitted with horizontal padding.
	if layout.SourceWidth != 1512 || layout.SourceHeight != 982 || math.Abs(layout.ContentY) > 1e-9 || math.Abs(layout.ContentHeight-900) > 1e-9 || layout.ContentX <= 0 {
		t.Fatalf("bad Retina fit %+v", layout)
	}
}
