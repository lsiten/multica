package cli

import "fmt"

// supportedProcessServiceRoles is the single source of truth for the profile
// opt-in process ownership roles. Each role is exposed only after its
// capability extraction has an independent, complete exclusive-wiring
// acceptance and the control parent holds no writer for that capability.
var supportedProcessServiceRoles = []string{
	"ai",
	"mirror",
	"application",
	"environment",
	"gateway",
}

// DefaultProcessServices returns every supported role. A profile that has never
// set process_services (nil) defaults to running all of them; an explicit empty
// list keeps the legacy in-process owners. The fail-closed fallback still keeps
// the in-process runner if a separate process can't start, so no task is lost
// or double-run.
func DefaultProcessServices() []string {
	return append([]string(nil), supportedProcessServiceRoles...)
}

// ValidateProcessServices allows only implementations already available in this build.
// A role is accepted here only once its extraction has an independent, complete
// exclusive-wiring acceptance so the production entrypoint can opt in without
// changing the legacy default. An explicit empty list is valid and keeps the
// legacy in-process owners; the daemon maps an UNSET (nil) value to
// DefaultProcessServices, so this validator sees the resolved, non-nil set.
func ValidateProcessServices(roles []string) error {
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
	return nil
}

// supportedProcessService is the profile opt-in set.
func supportedProcessService(role string) bool {
	for _, r := range supportedProcessServiceRoles {
		if r == role {
			return true
		}
	}
	return false
}
