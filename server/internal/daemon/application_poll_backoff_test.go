package daemon

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestApplicationPollBacksOffOutageAndEventRetriesImmediately(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); http.Error(w, "unavailable", 503) }))
	defer backend.Close()
	d, command := applicationManagerFixture(t, backend.URL)
	now := time.Now()
	d.reportNow = func() time.Time { return now }
	var workers sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	for range 10 {
		d.pollRuntimeApplications(t.Context(), semaphore, &workers, command.RuntimeID)
	}
	if requests.Load() != 1 {
		t.Fatalf("outage generated %d duplicate polls", requests.Load())
	}
	stateRaw, _ := d.applicationPollStates.Load(command.RuntimeID)
	state := stateRaw.(*applicationPollState)
	firstDeadline := state.next
	now = firstDeadline.Add(time.Nanosecond)
	d.pollRuntimeApplications(t.Context(), semaphore, &workers, command.RuntimeID)
	if requests.Load() != 2 || state.next.Sub(now) <= firstDeadline.Sub(time.Now()) {
		t.Fatal("failure retry did not increase backoff")
	}
	d.wakeApplications()
	d.pollRuntimeApplications(t.Context(), semaphore, &workers, command.RuntimeID)
	if requests.Load() != 3 {
		t.Fatal("new event did not bypass stale failure backoff")
	}
}

func TestApplicationIdlePollsAndHeartbeatDoNotRefetchUnchangedRuntimes(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte("[]")) }))
	defer backend.Close()
	d, command := applicationManagerFixture(t, backend.URL)
	other := uuid.NewString()
	var workers sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	for range 10 {
		d.pollRuntimeApplications(t.Context(), semaphore, &workers, command.RuntimeID)
		d.pollRuntimeApplications(t.Context(), semaphore, &workers, other)
	}
	if requests.Load() != 4 {
		t.Fatalf("idle repetition made %d calls instead of one sync/claim per runtime", requests.Load())
	}
	d.wakeApplications(command.RuntimeID)
	d.pollRuntimeApplications(t.Context(), semaphore, &workers, command.RuntimeID)
	d.pollRuntimeApplications(t.Context(), semaphore, &workers, other)
	if requests.Load() != 6 {
		t.Fatal("targeted hint refetched an unrelated runtime")
	}
	select {
	case <-d.applicationWake:
	default:
	}
	response := &HeartbeatResponse{ServerCapabilities: []string{protocol.DaemonCapabilityApplicationsV1}}
	d.applicationServerCapabilities.Delete(command.RuntimeID)
	d.handleHeartbeatActions(t.Context(), command.RuntimeID, response)
	select {
	case <-d.applicationWake:
	default:
		t.Fatal("first application capability did not wake discovery")
	}
	d.handleHeartbeatActions(t.Context(), command.RuntimeID, response)
	select {
	case <-d.applicationWake:
		t.Fatal("unchanged heartbeat repeated application discovery")
	default:
	}
}

func TestApplicationServiceWakeTargetsEverySelectedCohort(t *testing.T) {
	d, _ := applicationManagerFixture(t, "https://unused.invalid")
	first, second := uuid.NewString(), uuid.NewString()
	s := &applicationProcessService{daemon: d, cohorts: map[string]*applicationRuntimeCohort{first: {wake: make(chan struct{}, 1)}, second: {wake: make(chan struct{}, 1)}}}
	d.applicationWakeNotify = s.wakeCohorts
	raw, _ := json.Marshal(applicationWakeRequest{RuntimeIDs: []string{first}})
	if _, err := s.mutate(t.Context(), runtimeproc.Request{Operation: "application.wake", Payload: raw}); err != nil {
		t.Fatal(err)
	}
	if len(s.cohorts[first].wake) != 1 || len(s.cohorts[second].wake) != 0 {
		t.Fatal("targeted event was lost or woke a sibling runtime")
	}
	<-s.cohorts[first].wake
	d.wakeApplications()
	if len(s.cohorts[first].wake) != 1 || len(s.cohorts[second].wake) != 1 {
		t.Fatal("broadcast missed a runtime cohort")
	}
}

func TestApplicationEventWakeDoesNotWaitForBlockedBackend(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		http.Error(w, "unavailable", 503)
	}))
	defer backend.Close()
	d, command := applicationManagerFixture(t, backend.URL)
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.pollRuntimeApplications(t.Context(), make(chan struct{}, 4), &workers, command.RuntimeID)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("poll not entered")
	}
	woken := make(chan struct{})
	go func() { d.wakeApplications(); close(woken) }()
	select {
	case <-woken:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("local wake waited for remote request")
	}
	close(release)
	<-done
}
