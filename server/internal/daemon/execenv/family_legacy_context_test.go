package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFamilyLegacyCleanupDoesNotFollowChangedSidecarSymlinks(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[ancestor], func(t *testing.T) {
			root := t.TempDir()
			code := filepath.Join(root, "workdir")
			user := filepath.Join(code, "user-dir")
			if err := os.MkdirAll(user, 0700); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(user, "SKILL.md")
			writeFile(t, victim, "valuable user code")
			sidecar := filepath.Join(code, "skill.md")
			manifest := sidecarManifest{}
			if ancestor {
				dir := filepath.Join(code, "old-skill")
				if err := os.Symlink(user, dir); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				sidecar = filepath.Join(dir, "SKILL.md")
				manifest.Dirs = []string{dir}
			} else if err := os.Symlink(victim, sidecar); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			manifest.Files = []string{sidecar}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, sidecarManifestFile), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := CleanupLegacyFamilyContext(root, code); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, victim); got != "valuable user code" {
				t.Fatal("cleanup followed a changed symlink into user code")
			}
		})
	}
}
