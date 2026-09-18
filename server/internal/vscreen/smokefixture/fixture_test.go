package smokefixture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureNoGUIOptInFailsBeforeFilesystemOrNativeCalls(t *testing.T) {
	t.Setenv(guiEnvironment, "")
	if _, err := Prepare("/not-a-real-helper", "/not-an-evidence-directory"); err != ErrUnauthorized {
		t.Fatal(err)
	}
	if err := Run(); err != ErrUnauthorized {
		t.Fatal(err)
	}
	if _, err := Snapshot(); err != ErrUnauthorized {
		t.Fatal(err)
	}
}
func TestFixtureCopiesExactOwnedBinaryAndPrivateConfiguration(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	if err := os.WriteFile(source, []byte("test-owned fake binary, never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	app := &App{BundleID: "ai.multica.smoke." + strings.Repeat("a", 32), Directory: directory, nonce: strings.Repeat("a", 32), bundle: filepath.Join(directory, "Fixture.app")}
	if err := prepareFiles(source, app, "123:456"); err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(filepath.Join(app.bundle, "Contents", "MacOS", ExecutableName))
	if err != nil || string(copied) != "test-owned fake binary, never executed" {
		t.Fatal("source identity changed", err)
	}
	raw, err := readPrivate(filepath.Join(app.bundle, "Contents", "fixture.json"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	var cfg configuration
	if json.Unmarshal(raw, &cfg) != nil || cfg.Nonce != app.nonce || cfg.OwnerPID != os.Getpid() || cfg.OwnerStart != "123:456" || cfg.BinarySHA256 != app.BinarySHA256 {
		t.Fatal("invalid private launch config")
	}
	plist, err := os.ReadFile(filepath.Join(app.bundle, "Contents", "Info.plist"))
	if err != nil || !strings.Contains(string(plist), guiEnvironment) || !strings.Contains(string(plist), app.BundleID) {
		t.Fatal("missing per-fixture opt-in", err)
	}
}
func TestFixtureRejectsUnownedOrUnboundedReadback(t *testing.T) {
	app := &App{Directory: t.TempDir(), nonce: "expected"}
	path := filepath.Join(app.Directory, "readback.json")
	os.WriteFile(path, []byte(`{"nonce":"foreign","pid":1,"process_start":"start"}`), 0600)
	if _, err := app.Read(); err == nil {
		t.Fatal("foreign nonce accepted")
	}
	os.WriteFile(path, []byte(strings.Repeat("x", 8193)), 0600)
	if _, err := app.Read(); err == nil {
		t.Fatal("unbounded readback accepted")
	}
	os.Remove(path)
	os.Symlink("elsewhere", path)
	if _, err := app.Read(); err == nil {
		t.Fatal("symlink readback accepted")
	}
}
