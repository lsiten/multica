package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationCancelledStopResumesRealProcessWithoutRestart(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.test")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := d.executeApplication(ctx, command); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop := command
		stop.Action = "stop"
		stop.Generation = 4
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := d.executeApplication(ctx, stop); err != nil {
			t.Error(err)
		}
	})
	_, before, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	resume := command
	resume.Action = "resume"
	resume.Generation = 3
	observation, err := d.executeApplication(ctx, resume)
	if err != nil || observation.Generation != 3 || observation.ProcessState != "running" {
		t.Fatalf("resume=%+v error=%v", observation, err)
	}
	_, after, err := d.applicationRecord(resume)
	if err != nil || before.HostID != after.HostID {
		t.Fatalf("resume replaced the owner: %+v %v", after, err)
	}
	starts, err := os.ReadFile(command.Config.Environment["APPLICATION_START_FILE"])
	if err != nil || string(starts) != "start\n" {
		t.Fatalf("resume restarted service: %q %v", starts, err)
	}
	stale := command
	stale.Action = "stop"
	stale.Generation = 2
	if _, err := d.executeApplication(ctx, stale); err == nil {
		t.Fatal("late cancelled stop killed the resumed service")
	}
	response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(command.Config.Port))
	if err != nil {
		t.Fatal("resumed service became unreachable")
	}
	response.Body.Close()
}

func TestApplicationExpiredResumeReconcilesExistingProcessWhenRuntimeReconnects(t *testing.T) {
	var registry protocol.ApplicationRuntimeInstance
	var registryMu sync.RWMutex
	observed := make(chan protocol.ApplicationObservation, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sync"):
			registryMu.RLock()
			defer registryMu.RUnlock()
			if err := json.NewEncoder(w).Encode([]protocol.ApplicationRuntimeInstance{registry}); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(r.URL.Path, "/claim"):
			if err := json.NewEncoder(w).Encode([]protocol.ApplicationClaim{}); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(r.URL.Path, "/observe"):
			var input struct {
				Observation protocol.ApplicationObservation `json:"observation"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
				return
			}
			observed <- input.Observation
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("expired resume tried to claim or restart a service")
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	d, command := applicationManagerFixture(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := d.executeApplication(ctx, command); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.stopOwnedApplications)
	resume := command
	resume.Action = "resume"
	resume.Generation = 3
	registryMu.Lock()
	registry = protocol.ApplicationRuntimeInstance{DesiredState: "running", CanRestore: false, HasPendingOperation: false, Command: resume}
	registryMu.Unlock()
	var workers sync.WaitGroup
	d.pollRuntimeApplications(ctx, make(chan struct{}, 4), &workers, command.RuntimeID)
	workers.Wait()
	select {
	case observation := <-observed:
		if observation.Generation != 3 || observation.ProcessState != "running" {
			t.Fatalf("expired resume was not reconciled: %+v", observation)
		}
	default:
		t.Fatal("reconnected runtime did not report its existing service")
	}
	starts, err := os.ReadFile(command.Config.Environment["APPLICATION_START_FILE"])
	if err != nil || string(starts) != "start\n" {
		t.Fatalf("expired resume launched another service: %q %v", starts, err)
	}
}

func TestApplicationRegistryDoesNotExecuteStopAheadOfItsClaim(t *testing.T) {
	var registry protocol.ApplicationRuntimeInstance
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/sync") {
			if err := json.NewEncoder(w).Encode([]protocol.ApplicationRuntimeInstance{registry}); err != nil {
				t.Error(err)
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/claim") {
			if err := json.NewEncoder(w).Encode([]protocol.ApplicationClaim{}); err != nil {
				t.Error(err)
			}
			return
		}
		t.Error("pending registry executed a control or observation request")
		http.NotFound(w, r)
	}))
	defer server.Close()
	d, command := applicationManagerFixture(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := d.executeApplication(ctx, command); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop := command
		stop.Action = "stop"
		stop.Generation = 3
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := d.executeApplication(ctx, stop); err != nil {
			t.Error(err)
		}
	})
	stop := command
	stop.Action = "stop"
	stop.Generation = 2
	registry = protocol.ApplicationRuntimeInstance{DesiredState: "stopped", HasPendingOperation: true, Command: stop}
	var workers sync.WaitGroup
	d.pollRuntimeApplications(ctx, make(chan struct{}, 4), &workers, command.RuntimeID)
	workers.Wait()
	_, record, err := d.applicationRecord(command)
	if err != nil || record.Command.Generation != 1 {
		t.Fatalf("registry advanced an unclaimed stop: %+v %v", record, err)
	}
	response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(command.Config.Port))
	if err != nil {
		t.Fatal("unclaimed stop terminated the process")
	}
	response.Body.Close()
}
