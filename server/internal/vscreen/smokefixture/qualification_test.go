package smokefixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQualificationScopePrivateOwnershipAndReplay(t *testing.T) {
	for _, name := range []string{"valid", "expired", "parent", "owner_start", "binary", "directory_mode", "symlink", "manifest", "replay"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("MULTICA_RUN_VSCREEN_GUI_SMOKE", "1")
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			nonce := strings.Repeat("a", 32)
			directory := filepath.Join(root, "qualification-"+nonce)
			if err = os.MkdirAll(filepath.Join(directory, "Fixture.app/Contents/MacOS"), 0700); err != nil {
				t.Fatal(err)
			}
			bytes := []byte("owned fake executable never executed")
			sum := sha256.Sum256(bytes)
			scope := QualificationScope{ExpiresAt: time.Now().Add(time.Minute).UnixMilli(), Directory: directory, Nonce: nonce, BundleID: "ai.multica.smoke." + nonce, BinarySHA256: hex.EncodeToString(sum[:]), OwnerPID: 123, OwnerStart: "start"}
			scope.Resource.BackendIdentity = "https://fixture.invalid"
			scope.Resource.WorkspaceID = "ws"
			scope.Resource.RuntimeID = "runtime"
			scope.Resource.UID = uint32(os.Getuid())
			helper := filepath.Join(root, "helper")
			if err = os.WriteFile(helper, bytes, 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(scope.Executable(), bytes, 0700); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(scope)
			manifest := filepath.Join(directory, "Fixture.app/Contents/qualification.json")
			if err = os.WriteFile(manifest, raw, 0600); err != nil {
				t.Fatal(err)
			}
			parent := 123
			start := func(int) (string, error) { return "start", nil }
			switch name {
			case "expired":
				scope.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
			case "parent":
				parent++
			case "owner_start":
				start = func(int) (string, error) { return "reused", nil }
			case "binary":
				_ = os.WriteFile(scope.Executable(), []byte("other"), 0700)
			case "directory_mode":
				_ = os.Chmod(directory, 0755)
			case "symlink":
				_ = os.Remove(scope.Executable())
				_ = os.Symlink(helper, scope.Executable())
			case "manifest":
				_ = os.WriteFile(manifest, []byte(`{}`), 0600)
			}
			err = scope.validate(parent, helper, start)
			if (err == nil) != (name == "valid" || name == "replay") {
				t.Fatalf("%s: %v", name, err)
			}
			if name == "replay" {
				if scope.ConsumeQualificationHost() != nil || scope.ConsumeQualificationHost() == nil {
					t.Fatal("one-use host capability replayed")
				}
			}
		})
	}
}
func TestQualificationFileRefusesSymlinkAndOversize(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "owned")
	if err := os.WriteFile(target, []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := qualificationReadFile(target, 4, true); err == nil {
		t.Fatal("oversize accepted")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := qualificationReadFile(link, 10, true); err == nil {
		t.Fatal("symlink accepted")
	}
}
