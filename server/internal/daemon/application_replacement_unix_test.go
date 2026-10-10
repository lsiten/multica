//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"golang.org/x/sys/unix"
)

// TestApplicationReplacementHostProcess is an isolated fake owner, not a user
// application. Stop persists its receipt while a real host/source lease remains held.
func TestApplicationReplacementHostProcess(t *testing.T) {
	if os.Getenv("TASK17_FAKE_HOST") != "1" {
		return
	}
	path := os.Getenv("TASK17_RECORD")
	controlPath := os.Getenv("TASK17_CONTROL")
	record, err := applicationhost.ReadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(filepath.Join(filepath.Dir(path), "host.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		t.Fatal(err)
	}
	lock := os.NewFile(uintptr(fd), "host.lock")
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	defer lock.Close()
	lease, err := execenv.UseSharedDirectory(context.Background(), record.SourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lease.Finish(ctx, nil); err != nil {
			t.Error(err)
		}
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := "http://" + listener.Addr().String()
	if os.Getenv("TASK17_NO_CHANNEL") != "1" {
		record.Address = address
	}
	if err = applicationhost.WriteRecord(path, record); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(controlPath, []byte(address), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	stopCount := 0
	released := make(chan struct{})
	var releaseOnce sync.Once
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+record.Token {
			http.Error(w, "private fixture credential required", 401)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/fixture/release" {
			w.WriteHeader(204)
			releaseOnce.Do(func() { close(released) })
			return
		}
		if r.URL.Path == "/stop" && r.Method == http.MethodPost {
			var input struct {
				Generation int64 `json:"generation"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Generation < record.Command.Generation {
				http.Error(w, "stale generation", 409)
				return
			}
			record.Command.Generation = input.Generation
			record.Observation.Generation = input.Generation
			record.Observation.ProcessState = "stopped"
			record.Observation.HealthState = "unknown"
			if err := applicationhost.WriteRecord(path, record); err != nil {
				http.Error(w, "receipt write failed", 500)
				return
			}
			stopCount++
			if err := os.WriteFile(path+".stop-count", []byte(strconv.Itoa(stopCount)), 0600); err != nil {
				http.Error(w, "stop counter failed", 500)
				return
			}
			if err := os.WriteFile(path+".stop-ack", []byte("acknowledged while lock held\n"), 0600); err != nil {
				http.Error(w, "receipt marker failed", 500)
				return
			}
		} else if r.URL.Path != "/status" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(applicationhost.Status{HostID: record.HostID, InstanceID: record.Command.InstanceID, WorkspaceID: record.Command.WorkspaceID, RuntimeID: record.Command.RuntimeID, Observation: record.Observation})
	})}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	signals, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	select {
	case <-released:
	case <-signals.Done():
	}
	stopCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err = server.Shutdown(stopCtx); err != nil {
		t.Error(err)
	}
	if err = <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Error(err)
	}
}

type replacementHostFixture struct {
	path, address string
	record        applicationhost.Record
	process       *exec.Cmd
	exited        chan error
}

func task17Resource(t *testing.T, event string, fields map[string]any) {
	t.Helper()
	fields["event"] = event
	fields["scenario"] = t.Name()
	if directory := os.Getenv("MULTICA_TASK17_EVIDENCE_DIR"); directory != "" {
		stream, err := os.OpenFile(filepath.Join(directory, "resources.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewEncoder(stream).Encode(fields)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func startReplacementHostFixture(t *testing.T, d *Daemon, command protocol.ApplicationControlCommand, stopped, noChannel bool) replacementHostFixture {
	t.Helper()
	directory, err := d.applicationDirectory(command)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "host.json")
	source := filepath.Dir(command.Config.Environment["APPLICATION_START_FILE"])
	record, err := applicationhost.NewRecord(command, source)
	if err != nil {
		t.Fatal(err)
	}
	record.Observation.ProcessState = "running"
	record.Observation.HealthState = "healthy"
	if stopped {
		record.Observation.ProcessState = "stopped"
		record.Observation.HealthState = "unknown"
	}
	if err = applicationhost.WriteRecord(path, record); err != nil {
		t.Fatal(err)
	}
	controlPath := filepath.Join(directory, "fixture-control")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command(executable, "-test.run=^TestApplicationReplacementHostProcess$")
	process.Env, err = applicationHostEnvironment(command.Config)
	if err != nil {
		t.Fatal(err)
	}
	process.Env = append(process.Env, "TASK17_FAKE_HOST=1", "TASK17_RECORD="+path, "TASK17_CONTROL="+controlPath)
	if noChannel {
		process.Env = append(process.Env, "TASK17_NO_CHANNEL=1")
	}
	diagnostics, err := os.OpenFile(filepath.Join(directory, "fixture-process.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	process.Stdout = diagnostics
	process.Stderr = diagnostics
	task17Resource(t, "registered", map[string]any{"executable": executable, "record": path, "host_lock": filepath.Join(directory, "host.lock"), "source_root": source, "cleanup": "private release then exact process signal/reap; temp directory cleanup"})
	if err = process.Start(); err != nil {
		diagnostics.Close()
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- process.Wait(); diagnostics.Close() }()
	f := replacementHostFixture{path: path, record: record, process: process, exited: exited}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if f.address != "" {
			request, _ := http.NewRequestWithContext(ctx, "POST", f.address+"/fixture/release", nil)
			request.Header.Set("Authorization", "Bearer "+f.record.Token)
			if response, err := http.DefaultClient.Do(request); err == nil {
				response.Body.Close()
			}
		}
		cancel()
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			process.Process.Signal(syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(3 * time.Second):
				process.Process.Kill()
				<-exited
			}
		}
		task17Resource(t, "stopped", map[string]any{"pid": process.Process.Pid, "reaped": true, "record": path})
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, readErr := os.ReadFile(controlPath)
		if readErr == nil {
			f.address = string(raw)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake host did not announce private control")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The copy in cleanup must see the announced address too.
	task17Resource(t, "started", map[string]any{"pid": process.Process.Pid, "record": path, "address": f.address, "host_id": record.HostID})
	return f
}

func replacementLockHeld(path string) (bool, error) {
	file, err := os.OpenFile(filepath.Join(filepath.Dir(path), "host.lock"), os.O_RDWR, 0600)
	if err != nil {
		return false, err
	}
	defer file.Close()
	err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

func (f replacementHostFixture) release(t *testing.T) {
	t.Helper()
	request, err := http.NewRequest("POST", f.address+"/fixture/release", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+f.record.Token)
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatalf("release status=%d", response.StatusCode)
	}
}

func recordReplacementLaunches(t *testing.T, d *Daemon) *atomic.Int64 {
	t.Helper()
	counter := &atomic.Int64{}
	launcher := d.applicationHostLauncher
	var mu sync.Mutex
	var processes []*exec.Cmd
	d.applicationHostLauncher = func(path string) (*exec.Cmd, error) {
		counter.Add(1)
		cmd, err := launcher(path)
		if cmd != nil {
			mu.Lock()
			processes = append(processes, cmd)
			mu.Unlock()
		}
		return cmd, err
	}
	t.Cleanup(func() {
		mu.Lock()
		owned := append([]*exec.Cmd(nil), processes...)
		mu.Unlock()
		for _, cmd := range owned {
			if cmd.Process == nil {
				continue
			}
			pid := cmd.Process.Pid
			// PID is used only to reap this test-created process, never as launch proof.
			if syscall.Kill(pid, 0) == nil {
				cmd.Process.Signal(syscall.SIGTERM)
			}
			deadline := time.Now().Add(5 * time.Second)
			for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if syscall.Kill(pid, 0) == nil {
				cmd.Process.Kill()
				t.Errorf("fixture process %d exceeded cleanup deadline", pid)
			}
			task17Resource(t, "replacement_process_cleanup", map[string]any{"pid": pid, "exited": syscall.Kill(pid, 0) != nil})
		}
	})
	return counter
}

func TestApplicationReplacementDoesNotTrustStopReceiptBeforeLockExit(t *testing.T) {
	for _, noChannel := range []bool{false, true} {
		t.Run(fmt.Sprintf("stopped_without_channel=%t", noChannel), func(t *testing.T) {
			d, command := applicationManagerFixture(t, "http://unused.test")
			fake := startReplacementHostFixture(t, d, command, noChannel, noChannel)
			attempts := recordReplacementLaunches(t, d)
			command.Action = "restart"
			command.Generation = 2
			command.Revision = 2
			ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
			defer cancel()
			_, err := d.executeApplication(ctx, command)
			current, readErr := applicationhost.ReadRecord(fake.path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			held, lockErr := replacementLockHeld(fake.path)
			if lockErr != nil {
				t.Fatal(lockErr)
			}
			t.Logf("held=%t launch_attempts=%d old_host=%s current_host=%s error=%v", held, attempts.Load(), fake.record.HostID, current.HostID, err)
			if !held || attempts.Load() != 0 || current.HostID != fake.record.HostID || current.Token != fake.record.Token {
				t.Fatal("replacement launched or rewrote ownership while prior host retained its kernel lock")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("blocked replacement did not preserve deadline: %v", err)
			}
		})
	}
}

func stopReplacementServiceOnCleanup(t *testing.T, d *Daemon, command protocol.ApplicationControlCommand) {
	t.Helper()
	t.Cleanup(func() {
		stop := command
		stop.Action = "stop"
		stop.Generation++
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := d.executeApplication(ctx, stop); err != nil {
			t.Error(err)
		}
	})
}

func waitTask17(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("fixture observable state did not arrive")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestApplicationReplacementReleaseStartsExactlyOneHost(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.test")
	fake := startReplacementHostFixture(t, d, command, false, false)
	attempts := recordReplacementLaunches(t, d)
	command.Action = "restart"
	command.Generation = 2
	command.Revision = 2
	stopReplacementServiceOnCleanup(t, d, command)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		observation, err := d.executeApplication(ctx, command)
		if err == nil && observation.ProcessState != "running" {
			err = fmt.Errorf("replacement state=%s", observation.ProcessState)
		}
		result <- err
	}()
	waitTask17(t, func() bool { _, err := os.Stat(fake.path + ".stop-ack"); return err == nil })
	held, err := replacementLockHeld(fake.path)
	if err != nil || !held {
		t.Fatal("old lock was not retained after Stop ACK")
	}
	current, err := applicationhost.ReadRecord(fake.path)
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 0 || current.HostID != fake.record.HostID {
		t.Fatal("replacement published before release")
	}
	fake.release(t)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("replacement did not start after ownership release")
	}
	current, err = applicationhost.ReadRecord(fake.path)
	if err != nil {
		t.Fatal(err)
	}
	if current.HostID == fake.record.HostID || current.Command.Generation != 2 || current.Command.Revision != 2 {
		t.Fatal("replacement identity/version was not published")
	}
	if _, err = d.executeApplication(ctx, command); err != nil {
		t.Fatal(err)
	}
	stale := command
	stale.Generation = 1
	if _, err = d.executeApplication(ctx, stale); err == nil {
		t.Fatal("stale generation was accepted")
	}
	starts, err := os.ReadFile(command.Config.Environment["APPLICATION_START_FILE"])
	if err != nil || string(starts) != "start\n" || attempts.Load() != 1 {
		t.Fatalf("replacement attempts=%d starts=%q err=%v", attempts.Load(), starts, err)
	}
	task17Resource(t, "replacement_proved", map[string]any{"old_host_id": fake.record.HostID, "new_host_id": current.HostID, "start_count": attempts.Load(), "old_lock_held_after_stop_ack": held, "source_root": current.SourceRoot})
}

func installTask17GitGate(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	ready := filepath.Join(directory, "ready")
	gate := filepath.Join(directory, "gate")
	if err := unix.Mkfifo(gate, 0600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n: > \"$TASK17_GIT_READY\"\nIFS= read -r release < \"$TASK17_GIT_GATE\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(directory, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TASK17_GIT_READY", ready)
	t.Setenv("TASK17_GIT_GATE", gate)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	return ready, gate
}

func TestApplicationReplacementPublicationRechecksIdentityAndBothVersions(t *testing.T) {
	for _, change := range []string{"host_id", "generation", "revision", "desired_generation", "desired_revision"} {
		t.Run(change, func(t *testing.T) {
			d, command := applicationManagerFixture(t, "http://unused.test")
			fake := startReplacementHostFixture(t, d, command, true, true)
			fake.release(t)
			if err := applicationhost.WaitStopped(context.Background(), fake.path, fake.record.HostID); err != nil {
				t.Fatal(err)
			}
			attempts := recordReplacementLaunches(t, d)
			command.Action = "restart"
			command.Generation = 2
			command.Revision = 2
			d.rememberApplicationGeneration(command)
			ready, gate := installTask17GitGate(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := d.launchApplicationHost(ctx, command, fake.record.HostID); result <- err }()
			waitTask17(t, func() bool { _, err := os.Stat(ready); return err == nil })
			current, err := applicationhost.ReadRecord(fake.path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "host_id":
				replacement, err := applicationhost.NewRecord(current.Command, current.WorkDir)
				if err != nil {
					t.Fatal(err)
				}
				replacement.Observation.ProcessState = "stopped"
				current = replacement
			case "generation":
				current.Command.Generation = 3
				current.Observation.Generation = 3
			case "revision":
				current.Command.Generation = 2
				current.Observation.Generation = 2
				current.Command.Revision = 3
				current.Observation.Revision = 3
			case "desired_generation":
				newer := command
				newer.Generation = 3
				d.rememberApplicationGeneration(newer)
			case "desired_revision":
				newer := command
				newer.Revision = 3
				d.rememberApplicationGeneration(newer)
			}
			if err = applicationhost.WriteRecord(fake.path, current); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(gate, []byte("release\n"), 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("stale publication was accepted")
				}
			case <-ctx.Done():
				t.Fatal("publication did not resolve")
			}
			after, err := applicationhost.ReadRecord(fake.path)
			if err != nil {
				t.Fatal(err)
			}
			if attempts.Load() != 0 || after.HostID != current.HostID || after.Command.Generation != current.Command.Generation || after.Command.Revision != current.Command.Revision {
				t.Fatal("stale preparation rewrote the newer ownership/version")
			}
		})
	}
}

func TestApplicationReplacementFailureReceiptCannotOverwriteNewerFacts(t *testing.T) {
	for _, change := range []string{"host_id", "generation", "revision", "desired_generation", "desired_revision"} {
		t.Run(change, func(t *testing.T) {
			d, command := applicationManagerFixture(t, "http://unused.test")
			var expected applicationhost.Record
			d.applicationHostLauncher = func(path string) (*exec.Cmd, error) {
				current, err := applicationhost.ReadRecord(path)
				if err != nil {
					return nil, err
				}
				switch change {
				case "host_id":
					current.HostID = strings.Repeat("f", 32)
				case "generation":
					current.Command.Generation++
					current.Observation.Generation++
				case "revision":
					current.Command.Revision++
					current.Observation.Revision++
				case "desired_generation":
					newer := command
					newer.Generation++
					d.rememberApplicationGeneration(newer)
				case "desired_revision":
					newer := command
					newer.Revision++
					d.rememberApplicationGeneration(newer)
				}
				expected = current
				if err = applicationhost.WriteRecord(path, current); err != nil {
					return nil, err
				}
				return nil, errors.New("fixture failed before process start")
			}
			if _, err := d.executeApplication(context.Background(), command); err == nil {
				t.Fatal("fixture launch unexpectedly succeeded")
			}
			_, after, err := d.applicationRecord(command)
			if err != nil {
				t.Fatal(err)
			}
			if after.HostID != expected.HostID || after.Command.Generation != expected.Command.Generation || after.Command.Revision != expected.Command.Revision || after.Observation.ProcessState != "preparing" {
				t.Fatal("failure receipt replaced newer facts")
			}
		})
	}
}

func TestApplicationReplacementKeepsKnownUnstartedReceiptAndPriorBootRestore(t *testing.T) {
	for _, scenario := range []string{"unstarted", "prior_boot"} {
		t.Run(scenario, func(t *testing.T) {
			d, command := applicationManagerFixture(t, "http://unused.test")
			original := d.applicationHostLauncher
			if scenario == "unstarted" {
				d.applicationHostLauncher = func(string) (*exec.Cmd, error) { return nil, errors.New("fixture launch preparation failed") }
				if _, err := d.executeApplication(context.Background(), command); err == nil {
					t.Fatal("fixture error missing")
				}
				_, receipt, err := d.applicationRecord(command)
				if err != nil || receipt.Observation.ProcessState != "stopped" {
					t.Fatalf("known unstarted receipt lost: %v", err)
				}
				d.applicationHostLauncher = original
			} else {
				directory, err := d.applicationDirectory(command)
				if err != nil {
					t.Fatal(err)
				}
				old, err := applicationhost.NewRecord(command, filepath.Dir(command.Config.Environment["APPLICATION_START_FILE"]))
				if err != nil {
					t.Fatal(err)
				}
				old.BootID = "00000000-0000-4000-8000-000000000000"
				old.Observation.ProcessState = "running"
				if previous, err := applicationhost.PreviousBoot(old); err != nil || !previous {
					t.Fatalf("prior boot proof unavailable: %v", err)
				}
				if err = applicationhost.WriteRecord(filepath.Join(directory, "host.json"), old); err != nil {
					t.Fatal(err)
				}
			}
			attempts := recordReplacementLaunches(t, d)
			stopReplacementServiceOnCleanup(t, d, command)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			observation, err := d.executeApplication(ctx, command)
			if err != nil || observation.ProcessState != "running" || attempts.Load() != 1 {
				t.Fatalf("safe restore=%s attempts=%d err=%v", observation.ProcessState, attempts.Load(), err)
			}
		})
	}
}

func TestApplicationReplacementUnknownReadinessDoesNotBecomeStopped(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.test")
	directory, err := d.applicationDirectory(command)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "host.json")
	command.Config.Environment["TASK17_FAKE_HOST"] = "1"
	command.Config.Environment["TASK17_RECORD"] = path
	command.Config.Environment["TASK17_CONTROL"] = filepath.Join(directory, "fixture-control")
	command.Config.Environment["TASK17_NO_CHANNEL"] = "1"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d.applicationHostLauncher = func(string) (*exec.Cmd, error) {
		return exec.Command(executable, "-test.run=^TestApplicationReplacementHostProcess$"), nil
	}
	attempts := recordReplacementLaunches(t, d)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err = d.executeApplication(ctx, command); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unknown readiness did not retain deadline: %v", err)
	}
	record, err := applicationhost.ReadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	held, err := replacementLockHeld(path)
	if err != nil || !held || record.Observation.ProcessState == "stopped" {
		t.Fatal("unknown started host was treated as stopped")
	}
	newer := command
	newer.Generation++
	newer.Revision++
	newer.Action = "restart"
	if _, err = d.executeApplication(context.Background(), newer); err == nil {
		t.Fatal("unknown owner was replaced")
	}
	after, err := applicationhost.ReadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 1 || after.HostID != record.HostID {
		t.Fatal("unknown readiness caused a second launch or identity rewrite")
	}
}

func TestApplicationReplacementManualPrivateHTTPAndLockProof(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.test")
	fake := startReplacementHostFixture(t, d, command, false, false)
	attempts := recordReplacementLaunches(t, d)
	command.Action = "restart"
	command.Generation = 2
	command.Revision = 2
	stopReplacementServiceOnCleanup(t, d, command)
	type exchange struct {
		Method, Path string
		Status       int
		Response     any
	}
	var exchanges []exchange
	curl := func(method, path string, body any, want int) applicationhost.Status {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		config := "url = " + strconv.Quote(fake.address+path) + "\nrequest = " + strconv.Quote(method) + "\nheader = " + strconv.Quote("Authorization: Bearer "+fake.record.Token) + "\nheader = \"Content-Type: application/json\"\n"
		if method != "GET" {
			config += "data = " + strconv.Quote(string(payload)) + "\n"
		}
		cmd := exec.Command("curl", "--silent", "--show-error", "--config", "-", "--write-out", "\n%{http_code}")
		cmd.Stdin = strings.NewReader(config)
		raw, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		at := strings.LastIndexByte(string(raw), '\n')
		if at < 0 {
			t.Fatal("curl status missing")
		}
		status, err := strconv.Atoi(string(raw[at+1:]))
		if err != nil || status != want {
			t.Fatalf("curl %s status=%d err=%v", path, status, err)
		}
		var observation applicationhost.Status
		var response any
		if len(raw[:at]) > 0 {
			if err = json.Unmarshal(raw[:at], &observation); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw[:at], &response); err != nil {
				t.Fatal(err)
			}
		}
		exchanges = append(exchanges, exchange{method, path, status, response})
		return observation
	}
	running := curl("GET", "/status", nil, 200)
	stopped := curl("POST", "/stop", map[string]int64{"generation": 2}, 200)
	if running.HostID != fake.record.HostID || stopped.HostID != fake.record.HostID || running.Observation.ProcessState != "running" || stopped.Observation.ProcessState != "stopped" {
		t.Fatal("private HTTP identity/stop receipt mismatch")
	}
	held, err := replacementLockHeld(fake.path)
	if err != nil || !held {
		t.Fatal("Stop ACK did not precede kernel release")
	}
	unsettled, err := execenv.SharedDirectoryUnsettled(context.Background(), fake.record.SourceRoot)
	if err != nil || !unsettled {
		t.Fatalf("source participant not retained after ACK: %t %v", unsettled, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := d.executeApplication(ctx, command); result <- err }()
	waitTask17(t, func() bool {
		raw, err := os.ReadFile(fake.path + ".stop-count")
		return err == nil && string(raw) == "2"
	})
	before, err := applicationhost.ReadRecord(fake.path)
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 0 || before.HostID != fake.record.HostID {
		t.Fatal("manager replaced locked host after literal stop ACK")
	}
	curl("POST", "/fixture/release", nil, 204)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("replacement did not finish after release")
	}
	after, err := applicationhost.ReadRecord(fake.path)
	if err != nil {
		t.Fatal(err)
	}
	starts, err := os.ReadFile(command.Config.Environment["APPLICATION_START_FILE"])
	if err != nil || string(starts) != "start\n" || attempts.Load() != 1 || after.HostID == before.HostID {
		t.Fatal("release did not produce exactly one new real fixture service")
	}
	stop := command
	stop.Action = "stop"
	stop.Generation++
	if _, err = d.executeApplication(ctx, stop); err != nil {
		t.Fatal(err)
	}
	heldAfter, err := replacementLockHeld(fake.path)
	if err != nil || heldAfter {
		t.Fatal("replacement cleanup retained host lock")
	}
	sourceAfter, err := execenv.SharedDirectoryUnsettled(ctx, fake.record.SourceRoot)
	if err != nil || sourceAfter {
		t.Fatalf("replacement cleanup retained source participant: %v", err)
	}
	evidence := map[string]any{"invocation": "curl --silent --show-error --config - --write-out '\\n%{http_code}' (private credential and JSON on stdin)", "fake_host_pid": fake.process.Process.Pid, "record_path": fake.path, "host_lock": filepath.Join(filepath.Dir(fake.path), "host.lock"), "source_root": fake.record.SourceRoot, "old_host_id": before.HostID, "new_host_id": after.HostID, "stop_ack_while_host_lock_held": held, "source_participant_held_after_stop_ack": unsettled, "replacement_attempts_before_release": 0, "replacement_attempts_after_release": attempts.Load(), "real_service_start_lines": 1, "replacement_host_lock_released": !heldAfter, "source_participant_released": !sourceAfter, "exchanges": exchanges}
	if directory := os.Getenv("MULTICA_TASK17_EVIDENCE_DIR"); directory != "" {
		encoded, err := json.MarshalIndent(evidence, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, "http-lock-proof.json"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("literal HTTP Stop ACK retained host/source locks; publication blocked, release produced one host: %s -> %s", before.HostID, after.HostID)
}

func TestApplicationReplacementAbsentRecordCASAcrossControllers(t *testing.T) {
	first, command := applicationManagerFixture(t, "http://unused.test")
	attempts := recordReplacementLaunches(t, first)
	second := &Daemon{cfg: first.cfg, client: first.client, logger: first.logger, runtimeIndex: first.runtimeIndex, workspaces: first.workspaces, applicationHostLauncher: first.applicationHostLauncher}
	stopReplacementServiceOnCleanup(t, first, command)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	gate := make(chan struct{})
	results := make(chan error, 2)
	for _, controller := range []*Daemon{first, second} {
		go func(d *Daemon) { <-gate; _, err := d.executeApplication(ctx, command); results <- err }(controller)
	}
	close(gate)
	successes := 0
	for range 2 {
		select {
		case err := <-results:
			if err == nil {
				successes++
			}
		case <-ctx.Done():
			t.Fatal("competing controllers did not finish")
		}
	}
	if successes < 1 || attempts.Load() != 1 {
		t.Fatalf("absent-record CAS successes=%d launch_attempts=%d", successes, attempts.Load())
	}
	starts, err := os.ReadFile(command.Config.Environment["APPLICATION_START_FILE"])
	if err != nil || string(starts) != "start\n" {
		t.Fatalf("duplicate provider starts=%q err=%v", starts, err)
	}
}
