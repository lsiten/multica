//go:build !darwin && !linux

package daemon

import "testing"

func daemonFixtureEntry(_ *testing.M) {}
