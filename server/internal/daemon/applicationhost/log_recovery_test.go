package applicationhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationLogFailureDoesNotFailBusinessCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	log, err := openLog(path, "fixture", 4096, []string{"private-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := log.file.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestApplicationLogOutputFixture$")
	cmd.Env = append(os.Environ(), "MULTICA_LOG_OUTPUT_FIXTURE=1")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		t.Fatalf("log failure affected command: %v", err)
	}
	if err := log.close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "successful business [redacted]") || strings.Contains(string(raw), "private-secret") {
		t.Fatalf("redacted backlog was not replayed: %q, %v", raw, err)
	}
}

func TestApplicationLogOutputFixture(t *testing.T) {
	if os.Getenv("MULTICA_LOG_OUTPUT_FIXTURE") != "1" {
		return
	}
	fmt.Print("successful business private-secret\n")
	os.Exit(0)
}

func TestApplicationLogCanStartBeforeLogStorageRecovers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "service.log")
	log, err := openLog(path, "fixture", 4096, nil)
	if err != nil {
		t.Fatalf("unavailable log storage blocked local operation: %v", err)
	}
	if _, err := log.Write([]byte("retained log\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := log.close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "retained log\n" {
		t.Fatalf("backlog lost after recovery: %q, %v", raw, err)
	}
}

func TestApplicationLogStillRejectsSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "service.log")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := openLog(path, "fixture", 4096, nil); err == nil {
		t.Fatal("unsafe log destination accepted")
	}
}

func TestApplicationLogBacklogSurvivesHostRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	log, err := openLog(path, "first", 4096, []string{"private-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := log.file.Close(); err != nil {
		t.Fatal(err)
	}
	log.file = nil
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Write([]byte("pending private-secret\n")); err != nil {
		t.Fatal(err)
	}
	if err := log.close(); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path + ".pending")
	if err != nil || string(stored) != "pending [redacted]\n" {
		t.Fatalf("private pending bytes not preserved: %q, %v", stored, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	restarted, err := openLog(path, "second", 4096, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "pending [redacted]\n" {
		t.Fatalf("restart did not replay original bytes: %q, %v", raw, err)
	}
	if _, err := os.Stat(path + ".pending"); !os.IsNotExist(err) {
		t.Fatalf("acknowledged backlog still exists: %v", err)
	}
}

func TestStoppedApplicationLogReadReplaysPersistedBacklog(t *testing.T) {
	dir := t.TempDir()
	record := hostTestRecord(t)
	record.Observation.ProcessState = "stopped"
	path := filepath.Join(dir, "host.json")
	if err := WriteRecord(path, record); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "service.log.pending"), []byte("saved redacted output\n"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := ReadStoredLogs(path, "", 1024)
	if err != nil || page.Text != "saved redacted output\n" {
		t.Fatalf("stopped backlog was not recovered: %+v %v", page, err)
	}
}
