package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestTaskApprovalRequiresHumanInitiator(t *testing.T) {
	for _, task := range []Task{
		{InitiatorType: "agent", InitiatorID: "agent"},
		{InitiatorType: "member"},
		{InitiatorID: "user"},
	} {
		t.Run(task.InitiatorType+"/"+task.InitiatorID, func(t *testing.T) {
			d := &Daemon{}
			approved, err := d.requestTaskApproval(task)(t.Context(), agent.ApprovalRequest{})
			if approved || err == nil || err.Error() != "daemon: no human approval initiator" {
				t.Fatalf("approval without human initiator: approved=%v err=%v", approved, err)
			}
		})
	}
}
