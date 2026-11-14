package cli

import (
	"fmt"
	"os"
)

// supportedProcessServiceRoles is the single source of truth for the required
// process ownership roles. Each role has an independent child owner and the
// control parent holds no writer for that capability.
var supportedProcessServiceRoles = []string{
	"ai",
	"mirror",
	"application",
	"environment",
	"gateway",
}

// DefaultProcessServices returns every required process owner. The daemon is a
// multi-process runtime: there is no supported in-process owner configuration.
func DefaultProcessServices() []string {
	return append([]string(nil), supportedProcessServiceRoles...)
}

// ValidateProcessServices requires the complete child-owner set. An unset
// profile is resolved to DefaultProcessServices before this validator runs;
// explicit empty and partial sets are rejected so production cannot silently
// create an in-process capability owner.
func ValidateProcessServices(roles []string) error {
	if len(roles) == 0 {
		return fmt.Errorf("all required process services must be configured (required: ai, mirror, application, environment, gateway)")
	}
	seen := map[string]bool{}
	for _, role := range roles {
		if !supportedProcessService(role) {
			return fmt.Errorf("unsupported process service %q (supported: ai, mirror, application, environment, gateway)", role)
		}
		if seen[role] {
			return fmt.Errorf("duplicate process service %q", role)
		}
		seen[role] = true
	}
	if len(seen) != len(supportedProcessServiceRoles) {
		return fmt.Errorf("all required process services must be configured (required: ai, mirror, application, environment, gateway)")
	}
	return nil
}

// LegacyProcessServices reports whether a rejected value contains only the old
// owner roles and can therefore be safely normalized to the strict child-owner
// set. Unknown roles remain hard errors so a typo never gets silently hidden.
func LegacyProcessServices(roles []string) bool {
	for _, role := range roles {
		if !supportedProcessService(role) {
			return false
		}
	}
	return true
}

// MigrateLegacyProcessServices upgrades an old explicit empty/partial owner
// list in place. The original profile is retained once as a backup so a user
// can inspect or restore the pre-migration setting.
func MigrateLegacyProcessServices(profile string, cfg *CLIConfig) error {
	if cfg == nil || cfg.ProcessServices == nil || !LegacyProcessServices(*cfg.ProcessServices) {
		return fmt.Errorf("process_services is not a migratable legacy value")
	}
	path, err := CLIConfigPathForProfile(profile)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	backup := path + ".pre-multiprocess.bak"
	if _, statErr := os.Stat(backup); os.IsNotExist(statErr) {
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			return err
		}
	}
	roles := DefaultProcessServices()
	cfg.ProcessServices = &roles
	return SaveCLIConfigForProfile(*cfg, profile)
}

// supportedProcessService reports whether role is one of the required owners.
func supportedProcessService(role string) bool {
	for _, r := range supportedProcessServiceRoles {
		if r == role {
			return true
		}
	}
	return false
}
