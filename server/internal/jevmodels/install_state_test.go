package jevmodels

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistedInstallRecoveryDoesNotResumeOrOverwriteRevision(t *testing.T) {
	root := t.TempDir()
	raw, _ := json.Marshal(Status{ModelID: ModelID, Revision: Revision, State: "verifying"})
	if err := os.WriteFile(filepath.Join(root, "install-state.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(t.Context(), Config{RootDir: root, PythonPath: "/missing-python", PersistInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	status, err := manager.Status(ModelID)
	if err != nil || status.State != "interrupted" || status.Installed {
		t.Fatalf("recovery %+v %v", status, err)
	}
	if err = manager.Install(context.Background(), ModelID); err == nil {
		t.Fatal("missing engine unexpectedly installed")
	}
	content, err := os.ReadFile(filepath.Join(root, "install-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(content, &status) != nil || status.State != "failed" {
		t.Fatal("terminal install failure not durable")
	}
}
func TestShutdownReceiptRetainsCacheOwnership(t *testing.T) {
	root := t.TempDir()
	manager, err := New(t.Context(), Config{RootDir: root, PythonPath: "/missing"})
	if err != nil {
		t.Fatal(err)
	}
	err = manager.CloseWithReceipt(func() error {
		other, err := New(t.Context(), Config{RootDir: root, PythonPath: "/missing"})
		if err == nil {
			other.Close()
			t.Fatal("cache ownership released before shutdown receipt")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(t.Context(), Config{RootDir: root, PythonPath: "/missing"})
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
}
func TestMissingInstalledFilesAreNeverReportedInstalled(t *testing.T) {
	root := t.TempDir()
	raw, _ := json.Marshal(Status{ModelID: ModelID, Revision: Revision, State: "installed"})
	if err := os.WriteFile(filepath.Join(root, "install-state.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(t.Context(), Config{RootDir: root, PythonPath: "/missing", PersistInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	status, err := manager.Status(ModelID)
	if err != nil || status.State != "failed" || status.Installed {
		t.Fatalf("unverified installed claim %+v %v", status, err)
	}
	if err = manager.Remove(ModelID); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "install-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(content, &status) != nil || status.State != "not_installed" {
		t.Fatal("remove left installed receipt")
	}
}
