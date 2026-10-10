package repocache

import "strings"

// GitEnvironment keeps intentional Git transport and credential-helper settings
// without copying task-provider or platform account credentials into the physical
// service. Repository authorization belongs in explicit Git config entries.
func GitEnvironment(environ []string) []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
		"TMPDIR": true, "TMP": true, "TEMP": true, "SystemRoot": true, "SYSTEMROOT": true, "COMSPEC": true, "PATHEXT": true,
		"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TZ": true,
		"XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true,
		"APPDATA": true, "LOCALAPPDATA": true, "USERPROFILE": true, "HOMEDRIVE": true, "HOMEPATH": true,
		"SSH_AUTH_SOCK": true, "SSH_AGENT_PID": true, "SSH_ASKPASS": true, "SSH_ASKPASS_REQUIRE": true,
		"GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_SSH_VARIANT": true, "GIT_ASKPASS": true,
		"GIT_CONFIG": true, "GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true, "GIT_CONFIG_NOSYSTEM": true, "GIT_CONFIG_COUNT": true,
		"GIT_SSL_CAINFO": true, "GIT_SSL_CAPATH": true, "GIT_SSL_NO_VERIFY": true, "GIT_PROXY_COMMAND": true,
		"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "CURL_CA_BUNDLE": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
		"http_proxy": true, "https_proxy": true, "all_proxy": true, "no_proxy": true,
		"GCM_INTERACTIVE": true, "GCM_CREDENTIAL_STORE": true, "GCM_GUI_PROMPT": true,
		"GH_CONFIG_DIR": true,
	}
	result := make([]string, 0, len(environ))
	for _, entry := range environ {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		indexed := false
		for _, prefix := range []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"} {
			if suffix, ok := strings.CutPrefix(key, prefix); ok && suffix != "" {
				indexed = true
				for _, char := range suffix {
					if char < '0' || char > '9' {
						indexed = false
						break
					}
				}
			}
		}
		if allowed[key] || indexed {
			result = append(result, entry)
		}
	}
	return result
}
