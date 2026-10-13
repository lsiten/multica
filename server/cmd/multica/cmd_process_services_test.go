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
	if err := applyConfigSet(&cfg, "process_services", "ai"); err != nil || psLen(cfg) != 1 {
		t.Fatalf("ai opt-in %v %+v", err, cfg.ProcessServices)
	}
	if err := applyConfigSet(&cfg, "process_services", "ai,mirror"); err != nil || psLen(cfg) != 2 {
		t.Fatal("implemented mirror role unavailable")
	}
	if err := applyConfigSet(&cfg, "process_services", "ai,mirror,application"); err != nil || psLen(cfg) != 3 {
		t.Fatal("implemented application role unavailable")
	}
	for _, value := range []string{"task", "ai,ai", "mirror,mirror", "application,application", "ai,task"} {
		if err := applyConfigSet(&cfg, "process_services", value); err == nil {
			t.Fatalf("unsupported roles accepted: %s", value)
		}
	}
	// An explicit empty value stores a non-nil empty list (legacy in-process
	// opt-out); it must not be treated as "never set".
	if err := applyConfigSet(&cfg, "process_services", ""); err != nil || psLen(cfg) != 0 {
		t.Fatal("legacy selection did not clear")
	}
	if cfg.ProcessServices == nil {
		t.Fatal("explicit empty must be stored as a non-nil pointer")
	}
}
