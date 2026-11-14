package main

import (
	"github.com/multica-ai/multica/server/internal/cli"
	"testing"
)

// psLen returns the length of the stored process_services, dereferencing the
// pointer so an explicit empty list reads as 0 and a nil ("never set") pointer
// is not dereferenced.
func psLen(cfg cli.CLIConfig) int {
	if cfg.ProcessServices == nil {
		return 0
	}
	return len(*cfg.ProcessServices)
}

func TestProcessServicesConfigIsExplicitAndRejectsPretendRoles(t *testing.T) {
	cfg := cli.CLIConfig{}
	all := "ai,mirror,application,environment,gateway"
	if err := applyConfigSet(&cfg, "process_services", all); err != nil || psLen(cfg) != 5 {
		t.Fatalf("required process owners rejected: %v %+v", err, cfg.ProcessServices)
	}
	for _, value := range []string{"", "ai", "task", "ai,ai", "mirror,mirror", "application,application", "ai,task"} {
		if err := applyConfigSet(&cfg, "process_services", value); err == nil {
			t.Fatalf("invalid process service set accepted: %s", value)
		}
	}
}
