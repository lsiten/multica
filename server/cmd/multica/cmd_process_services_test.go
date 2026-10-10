package main

import (
	"github.com/multica-ai/multica/server/internal/cli"
	"testing"
)

func TestProcessServicesConfigIsExplicitAndRejectsPretendRoles(t *testing.T) {
	cfg := cli.CLIConfig{}
	if err := applyConfigSet(&cfg, "process_services", "ai"); err != nil || len(cfg.ProcessServices) != 1 {
		t.Fatalf("ai opt-in %v %+v", err, cfg.ProcessServices)
	}
	if err := applyConfigSet(&cfg, "process_services", "ai,mirror"); err != nil || len(cfg.ProcessServices) != 2 {
		t.Fatal("implemented mirror role unavailable")
	}
	if err := applyConfigSet(&cfg, "process_services", "ai,mirror,application"); err != nil || len(cfg.ProcessServices) != 3 {
		t.Fatal("implemented application role unavailable")
	}
	for _, value := range []string{"task", "ai,ai", "mirror,mirror", "application,application", "ai,task"} {
		if err := applyConfigSet(&cfg, "process_services", value); err == nil {
			t.Fatalf("unsupported roles accepted: %s", value)
		}
	}
	if err := applyConfigSet(&cfg, "process_services", ""); err != nil || len(cfg.ProcessServices) != 0 {
		t.Fatal("legacy selection did not clear")
	}
}
