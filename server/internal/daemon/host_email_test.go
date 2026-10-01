package daemon

import (
	"context"
	"testing"
)

func TestHostIdentityEmailRequiresSenderAndTaskBinding(t *testing.T) {
	task := Task{ID: "task", Agent: &AgentData{Identity: &AgentIdentityData{Email: "agent@example.test"}, CustomEnv: map[string]string{"SMTP_FROM_EMAIL": "wrong@example.test"}}}
	send := hostIdentityEmailInvoker(task, t.TempDir()+"/receipts")
	if err := send(context.Background(), "task", "to@example.test", "subject", "body"); err == nil {
		t.Fatal("mismatched sender was accepted")
	}
	if err := send(context.Background(), "other", "to@example.test", "subject", "body"); err == nil {
		t.Fatal("mismatched task was accepted")
	}
}
