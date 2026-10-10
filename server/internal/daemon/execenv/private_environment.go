package execenv

import "strings"

func privatePreparationVariableAllowed(key, value string, declared bool) bool {
	if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
		return false
	}
	upper := strings.ToUpper(key)
	if strings.HasPrefix(upper, "MULTICA_") && (strings.Contains(upper, "TOKEN") || strings.Contains(upper, "API_KEY") || strings.Contains(upper, "SECRET")) {
		return declared && strings.HasPrefix(value, "mat_") && upper != "MULTICA_CONTROL_TOKEN" && upper != "MULTICA_DAEMON_TOKEN" && upper != "MULTICA_ACCOUNT_TOKEN"
	}
	return true
}

// TaskPrivatePreparationEnvironment preserves provider-specific resolution only
// in the task's private helper. OpenClaw owns JSON5/includes/arbitrary ${VAR}
// interpolation, so its legacy environment remains private to that provider.
// Platform account/control credentials never accompany either helper or service.
func TaskPrivatePreparationEnvironment(provider string, environ []string, custom map[string]string) map[string]string {
	source := privatePreparationEnvironment(environ)
	if provider == "openclaw" {
		source = environ
	}
	result := make(map[string]string, len(source)+len(custom))
	for _, entry := range source {
		key, value, ok := strings.Cut(entry, "=")
		if ok && privatePreparationVariableAllowed(key, value, false) {
			result[key] = value
		}
	}
	for key, value := range custom {
		if privatePreparationVariableAllowed(key, value, true) {
			result[key] = value
		}
	}
	return result
}
