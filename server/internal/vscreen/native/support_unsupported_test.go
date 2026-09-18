//go:build !darwin || !cgo

package native

import "testing"

func TestSupportedRejectsUnsupportedBuild(t *testing.T) {
	if Supported() {
		t.Fatal("unsupported native build advertised virtual screens")
	}
}
