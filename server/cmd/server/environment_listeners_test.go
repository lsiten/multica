package main

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type environmentHintRecorder struct{ runtimes map[string]string }

func (recorder *environmentHintRecorder) NotifyPendingWork(runtimeID, kind string) {
	recorder.runtimes[runtimeID] = kind
}

func TestEnvironmentEventsWakeWorkspaceLocalRuntimes(t *testing.T) {
	fixture := testutil.New(testPool, testWorkspaceID, testUserID)
	local := fixture.Runtime(t, "environment-event-local", testutil.Cols{"runtime_mode": "local"})
	cloud := fixture.Runtime(t, "environment-event-cloud", testutil.Cols{"runtime_mode": "cloud"})
	bus := events.New()
	recorder := &environmentHintRecorder{runtimes: map[string]string{}}
	registerEnvironmentListeners(bus, db.New(testPool), recorder)
	for _, eventType := range []string{protocol.EventIssueUpdated, protocol.EventTaskCompleted, protocol.EventHumanRequestChanged, protocol.EventChatSessionDeleted} {
		if bus.SubscriberCount(eventType) != 1 {
			t.Fatalf("event %s not wired", eventType)
		}
		bus.Publish(events.Event{Type: eventType, WorkspaceID: testWorkspaceID})
		if recorder.runtimes[local] != protocol.PendingWorkKindEnvironment {
			t.Fatalf("event %s did not wake local runtime", eventType)
		}
		if _, notified := recorder.runtimes[cloud]; notified {
			t.Fatal("cloud runtime received local-directory hint")
		}
	}
}
