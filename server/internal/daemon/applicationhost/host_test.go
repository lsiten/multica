package applicationhost

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationHostFixtureService(t *testing.T) {
	if os.Getenv("APPLICATION_FIXTURE_SERVICE") != "1" {
		return
	}
	if os.Getenv("MULTICA_TOKEN") != "" || os.Getenv("MULTICA_TASK_TOKEN") != "" {
		t.Fatal("management credentials reached the application")
	}
	if file := os.Getenv("APPLICATION_FIXTURE_START_FILE"); file != "" {
		stream, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stream.WriteString("start\n"); err != nil {
			t.Fatal(err)
		}
		if err = stream.Close(); err != nil {
			t.Fatal(err)
		}
	}
	secret := os.Getenv("APPLICATION_FIXTURE_SECRET")
	if len(secret) > 3 {
		if _, err := fmt.Fprint(os.Stdout, "credential="+secret[:3]); err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintln(os.Stdout, secret[3:]+" service output follows without credentials"); err != nil {
			t.Fatal(err)
		}
	}
	server := &http.Server{Addr: "127.0.0.1:" + os.Getenv("PORT"), ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, "hello application"); err != nil {
			return
		}
	})}
	if err := server.ListenAndServe(); err != nil {
		t.Fatal(err)
	}
}

func hostTestRecord(t *testing.T) Record {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := protocol.DefaultApplicationConfig()
	config.ResourceID = "55555555-5555-4555-8555-555555555555"
	config.Command = []string{executable, "-test.run=^TestApplicationHostFixtureService$"}
	config.Port = port
	config.Environment = map[string]string{"APPLICATION_FIXTURE_SERVICE": "1", "APPLICATION_FIXTURE_START_FILE": filepath.Join(t.TempDir(), "starts")}
	config.LocalEnv = map[string]string{"APPLICATION_FIXTURE_SECRET": "APPLICATION_LOCAL_SECRET"}
	config.Health.Kind = "http"
	config.Health.Path = "/"
	config.Health.TimeoutSeconds = 5
	config.Health.IntervalSeconds = 1
	record, err := NewRecord(protocol.ApplicationControlCommand{InstanceID: "11111111-1111-4111-8111-111111111111", WorkspaceID: "22222222-2222-4222-8222-222222222222", RuntimeID: "33333333-3333-4333-8333-333333333333", ApplicationID: "44444444-4444-4444-8444-444444444444", Generation: 1, Revision: 1, Config: config}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func waitHostTest(t *testing.T, predicate func() bool) {
	t.Helper()
	timeout := time.NewTimer(8 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if predicate() {
			return
		}
		select {
		case <-timeout.C:
			t.Fatal("application host state did not converge")
		case <-ticker.C:
		}
	}
}

func TestApplicationHostRunsRecoversOwnershipAndStops(t *testing.T) {
	t.Setenv("APPLICATION_LOCAL_SECRET", "local-credential-not-for-upload")
	t.Setenv("MULTICA_TOKEN", "management-token-must-not-reach-service")
	t.Setenv("MULTICA_TASK_TOKEN", "task-token-must-not-reach-service")
	record := hostTestRecord(t)
	recordPath := filepath.Join(t.TempDir(), "host.json")
	if err := WriteRecord(recordPath, record); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, recordPath) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(8 * time.Second):
			t.Error("application host did not stop")
		}
	})
	var client *Client
	waitHostTest(t, func() bool {
		current, err := ReadRecord(recordPath)
		if err != nil || current.Address == "" {
			return false
		}
		client, err = NewClient(current)
		if err != nil {
			return false
		}
		status, err := client.Status(ctx)
		return err == nil && status.Observation.ProcessState == "running" && status.Observation.HealthState == "healthy"
	})
	response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(record.Command.Config.Port))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(raw) != "hello application" {
		t.Fatalf("real service response=%s error=%v", raw, err)
	}
	page, err := client.Logs(ctx, "", 65536)
	if err != nil || strings.Contains(page.Text, "local-credential-not-for-upload") || !strings.Contains(page.Text, "[redacted]") {
		t.Fatalf("redacted logs: %+v %v", page, err)
	}
	current, err := ReadRecord(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := NewClient(current)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := recovered.Status(ctx); err != nil || status.HostID != record.HostID {
		t.Fatalf("new daemon client could not prove ownership: %+v %v", status, err)
	}
	starts, err := os.ReadFile(record.Command.Config.Environment["APPLICATION_FIXTURE_START_FILE"])
	if err != nil || string(starts) != "start\n" {
		t.Fatalf("recovery restarted the service: %s %v", starts, err)
	}
	wrong := current
	wrong.Token = strings.Repeat("0", 64)
	unauthorized, err := NewClient(wrong)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = unauthorized.Stop(ctx, 2); err == nil {
		t.Fatal("unrelated credential stopped the service")
	}
	if _, err = recovered.Stop(ctx, 0); err == nil {
		t.Fatal("stale generation stopped the service")
	}
	if _, err = recovered.Stop(ctx, 2); err != nil {
		t.Fatal(err)
	}
	waitHostTest(t, func() bool {
		connection, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(record.Command.Config.Port), 100*time.Millisecond)
		if err != nil {
			return true
		}
		connection.Close()
		return false
	})
}

func TestApplicationHostPrivateRecordsAndCredentialReferences(t *testing.T) {
	t.Setenv("APPLICATION_LOCAL_SECRET", "fixture-only-secret")
	record := hostTestRecord(t)
	path := filepath.Join(t.TempDir(), "host.json")
	if err := WriteRecord(path, record); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecord(path); err == nil {
		t.Fatal("public host credential file accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := WriteRecord(target, record); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := ReadRecord(path); err == nil {
		t.Fatal("symlink host credential accepted")
	}
	if err := WriteRecord(path, record); err == nil {
		t.Fatal("symlink host credential replaced")
	}
	record.Address = "http://attacker.test:8080"
	if _, err := NewClient(record); err == nil {
		t.Fatal("non-local host address accepted")
	}
	config := protocol.DefaultApplicationConfig()
	config.LocalEnv = map[string]string{"KEY": "APPLICATION_MISSING_TEST_CREDENTIAL"}
	if _, _, err := Environment(config); err == nil {
		t.Fatal("missing local credential became an empty variable")
	}
}

func TestApplicationLogStreamsSafeOutputWithoutWaitingForLongCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	secret := "credential-" + strings.Repeat("x", 1024)
	log, err := openLog(path, "host", 4096, []string{secret, "abc", "abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	defer log.close()
	read := func(want string) {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != want {
			t.Fatalf("live log=%q want=%q err=%v", raw, want, err)
		}
	}
	for _, step := range []struct{ chunk, want string }{
		{"service ready\n", "service ready\n"},
		{"abc", "service ready\n"},
		{"def\n", "service ready\n[redacted]\n"},
		{secret[:25], "service ready\n[redacted]\n"},
		{secret[25:] + "\ndone\n", "service ready\n[redacted]\n[redacted]\ndone\n"},
	} {
		if _, err := log.Write([]byte(step.chunk)); err != nil {
			t.Fatal(err)
		}
		read(step.want)
	}
}

func TestApplicationLogRedactsSplitSecretsAndReportsRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	log, err := openLog(path, "host", 128, []string{"split-secret", "aa"})
	if err != nil {
		t.Fatal(err)
	}
	defer log.close()
	for _, chunk := range []string{"begin split-", "secret end\n", strings.Repeat("a", 4096), "end of output\n"} {
		if _, err := log.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := log.page("host:0:0", 128)
	if err != nil || !page.Gap || len(page.Text) > 128 {
		t.Fatalf("rotation=%+v err=%v", page, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > 128 || strings.Contains(string(raw), "split-secret") || strings.Contains(string(raw), "aa") {
			t.Fatalf("unbounded or unredacted log %s: %s", entry.Name(), raw)
		}
	}
}

func TestApplicationStoredLogsPreserveCursorAndRejectSymlinks(t *testing.T) {
	record := hostTestRecord(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "service.log")
	log, err := openLog(path, record.HostID, 64, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Write([]byte(strings.Repeat("x", 64) + "retained output\n")); err != nil {
		t.Fatal(err)
	}
	first, err := log.page("", 8)
	if err != nil || first.Text != "retained" {
		t.Fatalf("first page=%+v %v", first, err)
	}
	if err := log.close(); err != nil {
		t.Fatal(err)
	}
	record.LogRotation = log.rotation
	record.Observation.ProcessState = "stopped"
	recordPath := filepath.Join(directory, "host.json")
	if err := WriteRecord(recordPath, record); err != nil {
		t.Fatal(err)
	}
	second, err := ReadStoredLogs(recordPath, first.Cursor, 64)
	if err != nil || second.Text != " output\n" || second.Gap {
		t.Fatalf("stopped output lost cursor: %+v %v", second, err)
	}
	gap, err := ReadStoredLogs(recordPath, record.HostID+":0:0", 64)
	if err != nil || !gap.Gap || gap.Text != "retained output\n" {
		t.Fatalf("rotation gap lost: %+v %v", gap, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(other, []byte("unrelated private data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadStoredLogs(recordPath, "", 64); err == nil {
		t.Fatal("service log symlink exposed an unrelated file")
	}
}
