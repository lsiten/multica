package appcontrol

import (
	"math"
	"testing"
)

func TestBoundsRejectPartialWindowAndInvalidNumbers(t *testing.T) {
	display := Bounds{X: 1600, Y: 0, Width: 1600, Height: 900}
	if !display.Contains(Bounds{X: 1610, Y: 10, Width: 400, Height: 300}) {
		t.Fatal("contained window rejected")
	}
	if display.Contains(Bounds{X: 1599, Y: 0, Width: 400, Height: 300}) {
		t.Fatal("partial real-screen window accepted")
	}
	for _, n := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if display.Contains(Bounds{X: n, Y: 0, Width: 20, Height: 20}) {
			t.Fatal("nonfinite window accepted")
		}
	}
}
