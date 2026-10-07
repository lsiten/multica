package application

import (
	"encoding/json"
	"testing"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestOperationDeadlineIncludesPreparationReadinessRetriesAndQueueGrace(t *testing.T) {
	created := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	config := protocol.DefaultApplicationConfig()
	config.Prepare = []protocol.ApplicationCommand{{Args: []string{"prepare"}, TimeoutSeconds: 450}}
	config.Restart.Enabled = true
	command, err := json.Marshal(protocol.ApplicationControlCommand{Config: config})
	if err != nil {
		t.Fatal(err)
	}
	deadline, err := operationDeadline(created, []db.ApplicationOperationStep{{Command: command}})
	want := created.Add(3*time.Hour + 21*time.Minute + 45*time.Second)
	if err != nil || !deadline.Equal(want) {
		t.Fatalf("preparation and bounded retries deadline=%s want=%s error=%v", deadline, want, err)
	}
	config.Prepare = make([]protocol.ApplicationCommand, 16)
	for index := range config.Prepare {
		config.Prepare[index] = protocol.ApplicationCommand{Args: []string{"prepare"}, TimeoutSeconds: 1800}
	}
	config.Health.TimeoutSeconds = 600
	config.Restart.MaxAttempts = 10
	config.Restart.DelaySeconds = 300
	command, err = json.Marshal(protocol.ApplicationControlCommand{Config: config})
	if err != nil {
		t.Fatal(err)
	}
	deadline, err = operationDeadline(created, []db.ApplicationOperationStep{{Command: command}, {Command: command}})
	want = created.Add(3*time.Hour + 2*(10*time.Hour+50*time.Minute))
	if err != nil || !deadline.Equal(want) {
		t.Fatalf("long preparation chain was cut short: deadline=%s want=%s error=%v", deadline, want, err)
	}
	if _, err := operationDeadline(created, []db.ApplicationOperationStep{{Command: []byte("invalid-json")}}); err == nil {
		t.Fatal("corrupt command received an execution budget")
	}
}
