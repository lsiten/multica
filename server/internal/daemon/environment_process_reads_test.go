package daemon

import (
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestEnvironmentReadCapabilitiesRejectWrites(t *testing.T) {
	owner := &environmentProcessService{physical: &environmentPhysicalOwner{}}
	c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.ReadCapabilities = []string{"review.read", "environment.command"}
		config.ReadHandler = owner.read
	})
	for _, test := range []struct {
		name, operation, payload string
	}{
		{"review merge", "review.read", `{"action":"merge"}`},
		{"review edit", "review.read", `{"action":"edit"}`},
		{"environment policy", "environment.command", `{"command":{"action":"policy_update"}}`},
		{"environment cleanup", "environment.command", `{"command":{"action":"operation_start"}}`},
		{"disguised environment write", "review.read", `{"action":"read","environment":{"action":"operation_start"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := c.process.Client.Read(t.Context(), test.operation, []byte(test.payload))
			var rejection *runtimeproc.Error
			if !errors.As(err, &rejection) || rejection.Code != "environment_failed" {
				t.Fatalf("write entered the read capability: %v", err)
			}
		})
	}
	record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Operations) != 0 {
		t.Fatalf("rejected reads consumed %d mutation receipts", len(record.Operations))
	}
}
