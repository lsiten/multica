//go:build !darwin && !linux

package daemon

import "testing"

// daemonFixtureEntry is a no-op on platforms without the vscreen host fixture
// build. The real dispatch lives in vscreen_host_fixture_test.go (darwin||linux).
func daemonFixtureEntry(m *testing.M) {}
