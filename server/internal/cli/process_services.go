package cli

import "fmt"

// ValidateProcessServices allows only implementations already available in this build.
// Empty keeps the legacy in-process owners; a role is added here only once its
// extraction has an independent, complete exclusive-wiring acceptance so the
// production entrypoint can opt in without changing the legacy default.
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

// supportedProcessService is the profile opt-in set. Each role is exposed only
// after its capability extraction has an independent acceptance and the control
// parent holds no writer for that capability; the default (empty) stays legacy.
func supportedProcessService(role string) bool {
	switch role {
	case "ai", "mirror", "application", "environment", "gateway":
		return true
	default:
		return false
	}
}
